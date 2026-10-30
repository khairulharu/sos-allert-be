package usecase

import (
	"context"
	"errors"
	"testing"

	"golang-clean-architecture/internal/ai"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
)

// stubTriager returns a fixed result (or error) for AI integration tests.
type stubTriager struct {
	result ai.Result
	err    error
}

func (s stubTriager) Triage(ctx context.Context, in ai.Input) (ai.Result, error) {
	return s.result, s.err
}

func aiTestSetup(t *testing.T, triager AITriager) (*TriggerSOSUseCase, *fakeLocationRepository, *fakeAlertRepository) {
	t.Helper()
	sender := &entity.LocationCache{UserId: "sender", Longitude: 115.13, Latitude: -8.65}
	nearby := []string{"sender", "user-b", "user-c"}
	lastSeen := map[string]int64{}
	for _, id := range nearby {
		lastSeen[id] = freshLastSeen()
	}
	locRepo := &fakeLocationRepository{sender: sender, nearby: nearby, lastSeenMs: lastSeen}
	alertRepo := &fakeAlertRepository{}
	uc := newUseCaseWithDeps(TriggerSOSDeps{
		Location: locRepo, Alerts: alertRepo, Notifier: &fakeNotifier{},
		AITriager: triager, GracePeriod: 0,
	})
	return uc, locRepo, alertRepo
}

func TestTriggerWithAITriageStoresFieldsAndExpands(t *testing.T) {
	triager := stubTriager{result: ai.Result{
		Category: ai.CategoryDrowning, EmergencyType: "MEDICAL", Severity: 5,
		Confidence: 0.9, Language: "id", SummaryID: "Orang tenggelam.", SummaryEN: "Drowning.",
		Source: "llm",
	}}
	uc, locRepo, alertRepo := aiTestSetup(t, triager)
	req := &model.TriggerSOSRequest{Message: "Tolong, ada orang tenggelam di pantai"}
	lat, lng := -8.65, 115.13
	req.Latitude, req.Longitude = &lat, &lng
	result, err := uc.ExecuteWithRequest(context.Background(), "sender", req, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest() error = %v", err)
	}
	if result.Triage == nil || result.Triage.Category != ai.CategoryDrowning || result.Triage.Severity != 5 {
		t.Errorf("triage = %+v", result.Triage)
	}
	if result.Guide == nil || result.Guide.Category != ai.CategoryDrowning {
		t.Errorf("guide = %+v", result.Guide)
	}
	if alertRepo.lastAlert.AICategory != ai.CategoryDrowning || alertRepo.lastAlert.AIOriginalText != req.Message {
		t.Errorf("stored alert AI fields = %+v", alertRepo.lastAlert)
	}
	if alertRepo.lastAlert.EmergencyType != "MEDICAL" {
		t.Errorf("emergency type = %q, want MEDICAL from triage", alertRepo.lastAlert.EmergencyType)
	}
	// Severity 5 aims for 5 recipients; only 2 fresh users exist, so
	// all three radii must be probed.
	if locRepo.geoSearchCalls != 3 {
		t.Errorf("GeoSearch calls = %d, want 3 for critical severity", locRepo.geoSearchCalls)
	}
}

func TestTriggerLowSeverityStaysAtOneKilometre(t *testing.T) {
	triager := stubTriager{result: ai.Result{
		Category: ai.CategoryOther, EmergencyType: "DANGER", Severity: 2,
		Confidence: 0.9, SummaryID: "Laporan ringan.", SummaryEN: "Minor report.", Source: "rules",
	}}
	uc, locRepo, _ := aiTestSetup(t, triager)
	req := &model.TriggerSOSRequest{Message: "tolong, ada yang butuh bantuan kecil"}
	lat, lng := -8.65, 115.13
	req.Latitude, req.Longitude = &lat, &lng
	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", req, ""); err != nil {
		t.Fatalf("ExecuteWithRequest() error = %v", err)
	}
	if locRepo.geoSearchCalls != 1 || locRepo.radii[0] != 1000 {
		t.Errorf("GeoSearch radii = %v, want only [1000] for low severity", locRepo.radii)
	}
}

func TestTriggerTriageErrorFailsOpen(t *testing.T) {
	uc, _, alertRepo := aiTestSetup(t, stubTriager{err: errors.New("triage down")})
	req := &model.TriggerSOSRequest{Message: "help"}
	lat, lng := -8.65, 115.13
	req.Latitude, req.Longitude = &lat, &lng
	result, err := uc.ExecuteWithRequest(context.Background(), "sender", req, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest() error = %v, want SOS to proceed without triage", err)
	}
	if result.Triage != nil {
		t.Errorf("triage = %+v, want nil on triage failure", result.Triage)
	}
	if !alertRepo.createCalled {
		t.Error("alert was not created")
	}
}

func TestTriggerTriageOverrideWins(t *testing.T) {
	// Even with a triager that would say otherwise, the user's
	// confirmed correction must be stored.
	triager := stubTriager{result: ai.Result{
		Category: ai.CategoryOther, EmergencyType: "DANGER", Severity: 2,
		Confidence: 0.9, SummaryID: "Salah.", SummaryEN: "Wrong.", Source: "llm",
	}}
	uc, _, alertRepo := aiTestSetup(t, triager)
	lat, lng := -8.65, 115.13
	req := &model.TriggerSOSRequest{
		Message:   "kecelakaan motor, berdarah",
		Latitude:  &lat,
		Longitude: &lng,
		TriageOverride: &model.TriageOverrideRequest{
			Category: ai.CategoryAccidentTraffic, Severity: 5,
			SummaryID: "Kecelakaan motor, korban berdarah.", SummaryEN: "Motorbike accident, victim bleeding.",
		},
	}
	result, err := uc.ExecuteWithRequest(context.Background(), "sender", req, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest() error = %v", err)
	}
	if result.Triage == nil || result.Triage.Source != "override" || result.Triage.Category != ai.CategoryAccidentTraffic {
		t.Errorf("triage = %+v", result.Triage)
	}
	if alertRepo.lastAlert.AISource != "override" || alertRepo.lastAlert.EmergencyType != "ACCIDENT" {
		t.Errorf("stored alert = %+v", alertRepo.lastAlert)
	}
}

func TestPreviewTriage(t *testing.T) {
	uc, _, _ := aiTestSetup(t, stubTriager{result: ai.Result{
		Category: ai.CategoryFire, EmergencyType: "DANGER", Severity: 4,
		Confidence: 0.8, SummaryID: "Kebakaran.", SummaryEN: "Fire.", Source: "rules",
	}})
	preview, err := uc.PreviewTriage(context.Background(), &model.TriagePreviewRequest{Message: "kebakaran"})
	if err != nil {
		t.Fatalf("PreviewTriage() error = %v", err)
	}
	if preview.Triage.Category != ai.CategoryFire || preview.Guide == nil || len(preview.Guide.StepsID) == 0 {
		t.Errorf("preview = %+v", preview)
	}
	if _, err := uc.PreviewTriage(context.Background(), &model.TriagePreviewRequest{Message: ""}); err == nil {
		t.Error("empty preview message must be rejected")
	}
}

func TestDispatchPlanForSeverity(t *testing.T) {
	radii := []float64{1000, 2000, 5000}
	if p := DispatchPlanForSeverity(0, radii, 3, 50); len(p.Radii) != 3 || p.MinRecipients != 3 {
		t.Errorf("no-triage plan = %+v", p)
	}
	if p := DispatchPlanForSeverity(2, radii, 3, 50); len(p.Radii) != 1 || p.MaxRecipients != 10 {
		t.Errorf("low-severity plan = %+v", p)
	}
	if p := DispatchPlanForSeverity(5, radii, 3, 50); p.MinRecipients != 5 || len(p.Radii) != 3 {
		t.Errorf("critical plan = %+v", p)
	}
}
