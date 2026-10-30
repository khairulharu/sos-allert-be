package usecase

import (
	"context"
	"strings"
	"time"

	"golang-clean-architecture/internal/ai"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/gofiber/fiber/v2"
)

// AITriager classifies a short panic report. Satisfied by *ai.Service.
// Implementations must be fail-open: a provider failure falls back
// to rule triage inside the service, and an error here must never
// block an SOS (the caller proceeds without triage).
type AITriager interface {
	Triage(ctx context.Context, in ai.Input) (ai.Result, error)
}

// DispatchPlan is the severity-aware fan-out for one incident:
// which radii to probe, how many fresh recipients to aim for, and
// the cap. Severity drives the plan so critical incidents reach
// further and lighter ones do not alarm the whole neighbourhood.
type DispatchPlan struct {
	Radii         []float64
	MinRecipients int
	MaxRecipients int
}

// DispatchPlanForSeverity derives a plan from triage severity.
// Severity 0 means "no triage" and keeps the README defaults.
func DispatchPlanForSeverity(severity int, radii []float64, minRecipients, maxRecipients int) DispatchPlan {
	if len(radii) == 0 {
		radii = []float64{1000, 2000, 5000}
	}
	if minRecipients <= 0 {
		minRecipients = 3
	}
	if maxRecipients <= 0 {
		maxRecipients = 50
	}
	plan := DispatchPlan{Radii: radii, MinRecipients: minRecipients, MaxRecipients: maxRecipients}
	switch {
	case severity <= 0 || severity == 3:
		// Defaults.
	case severity <= 2:
		plan.Radii = radii[:1]
		if plan.MaxRecipients > 10 {
			plan.MaxRecipients = 10
		}
	case severity == 4:
		if plan.MinRecipients < 4 {
			plan.MinRecipients = 4
		}
	case severity >= 5:
		if plan.MinRecipients < 5 {
			plan.MinRecipients = 5
		}
	}
	return plan
}

// PreviewTriage classifies a report without creating an alert, so
// the frontend can show the AI result card and let the user correct
// it before confirming. It creates no alert, touches no idempotency
// key, and does not count against the SOS rate limit.
func (u *TriggerSOSUseCase) PreviewTriage(ctx context.Context, req *model.TriagePreviewRequest) (*model.TriagePreviewResponse, error) {
	if req == nil {
		req = &model.TriagePreviewRequest{}
	}
	if err := u.validate(req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Message) == "" {
		return nil, fiber.ErrBadRequest
	}
	if u.Deps.AITriager == nil {
		return nil, fiber.NewError(fiber.StatusServiceUnavailable, "AI triage is not configured")
	}
	tctx, cancel := context.WithTimeout(ctx, u.triageTimeout())
	defer cancel()
	result, err := u.Deps.AITriager.Triage(tctx, ai.Input{Text: req.Message, AreaLabel: req.AreaLabel})
	if err != nil {
		u.Log.WithError(err).Warn("AI triage preview failed")
		return nil, fiber.ErrBadGateway
	}
	triage := triageResponseFromResult(result, req.Message)
	return &model.TriagePreviewResponse{Triage: triage, Guide: guideResponseFromCategory(result.Category)}, nil
}

func (u *TriggerSOSUseCase) triageTimeout() time.Duration {
	if u.Deps.AITimeout > 0 {
		return u.Deps.AITimeout
	}
	return 3 * time.Second
}

// runTriage resolves the triage for a trigger: the user's confirmed
// override wins, otherwise the AI service classifies the message.
// It returns nil when there is no message and no override, and nil
// (fail-open) when triage errors, so SOS creation never depends on AI.
func (u *TriggerSOSUseCase) runTriage(ctx context.Context, req *model.TriggerSOSRequest) *ai.Result {
	if req.TriageOverride != nil {
		ov := req.TriageOverride
		r := ai.Validate(ai.Result{
			Category:   ov.Category,
			Severity:   ov.Severity,
			Confidence: 1,
			SummaryID:  ov.SummaryID,
			SummaryEN:  ov.SummaryEN,
			Needs:      ai.NeedsForCategory(ov.Category),
			Source:     "override",
		})
		return &r
	}
	if strings.TrimSpace(req.Message) == "" || u.Deps.AITriager == nil {
		return nil
	}
	tctx, cancel := context.WithTimeout(ctx, u.triageTimeout())
	defer cancel()
	result, err := u.Deps.AITriager.Triage(tctx, ai.Input{Text: req.Message, AreaLabel: req.AreaLabel})
	if err != nil {
		u.Log.WithError(err).Warn("AI triage failed, proceeding without triage")
		return nil
	}
	return &result
}

func triageResponseFromResult(r ai.Result, original string) *model.TriageResponse {
	return &model.TriageResponse{
		Category:           r.Category,
		EmergencyType:      r.EmergencyType,
		Severity:           r.Severity,
		Confidence:         r.Confidence,
		Language:           r.Language,
		SummaryID:          r.SummaryID,
		SummaryEN:          r.SummaryEN,
		Keywords:           r.Keywords,
		Needs:              r.Needs,
		NeedsClarification: r.NeedsClarification,
		Source:             r.Source,
		OriginalText:       original,
	}
}

func triageResponseFromAlert(a *entity.Alert) *model.TriageResponse {
	if a == nil || a.AICategory == "" {
		return nil
	}
	return &model.TriageResponse{
		Category:           a.AICategory,
		EmergencyType:      a.EmergencyType,
		Severity:           a.AISeverity,
		Confidence:         a.AIConfidence,
		Language:           a.AILanguage,
		SummaryID:          a.AISummaryID,
		SummaryEN:          a.AISummaryEN,
		NeedsClarification: a.AINeedsClarification,
		Source:             a.AISource,
		OriginalText:       a.AIOriginalText,
	}
}

func guideResponseFromCategory(category string) *model.GuideResponse {
	g := ai.GuideFor(category)
	return &model.GuideResponse{
		Category:     g.Category,
		TitleID:      g.TitleID,
		TitleEN:      g.TitleEN,
		StepsID:      g.StepsID,
		StepsEN:      g.StepsEN,
		DisclaimerID: g.DisclaimerID,
		DisclaimerEN: g.DisclaimerEN,
	}
}
