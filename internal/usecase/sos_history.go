package usecase

import (
	"context"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/gofiber/fiber/v2"
)

// maxHistoryPage bounds the paging OFFSET (page * size must stay far
// below the int range so the multiplication cannot overflow).
const maxHistoryPage = 100000

// ListHistory returns one page of the caller's incident history plus the
// total row count behind the paging metadata. role=sent lists incidents
// the caller triggered; role=received lists incidents they were notified
// for (alert_recipients), regardless of status.
func (u *TriggerSOSUseCase) ListHistory(ctx context.Context, request *model.SearchAlertHistoryRequest) ([]*model.AlertDetailResponse, int64, error) {
	if request == nil {
		request = &model.SearchAlertHistoryRequest{}
	}
	if err := u.validate(request); err != nil {
		return nil, 0, err
	}
	// Pointers don't play well with gtefield, so compare the window here.
	if request.FromMs != nil && request.ToMs != nil && *request.ToMs < *request.FromMs {
		return nil, 0, fiber.ErrBadRequest
	}
	// Defence in depth: when validation is disabled (nil validator) the
	// controller defaults could still be zero, and a zero Size would turn
	// Limit(0) into "no limit" and a zero Page into a negative OFFSET.
	if request.Page < 1 {
		request.Page = 1
	}
	// (Page-1)*Size must not overflow: a negative OFFSET makes GORM drop
	// the clause and answer with page 1, which hides the mistake.
	if request.Page > maxHistoryPage {
		return nil, 0, fiber.ErrBadRequest
	}
	if request.Size < 1 || request.Size > 100 {
		request.Size = 20
	}

	var (
		alerts []*entity.Alert
		total  int64
		err    error
	)
	if request.Role == "received" {
		alerts, total, err = u.Deps.Alerts.ListHistoryForRecipient(ctx, u.DB, request)
	} else {
		alerts, total, err = u.Deps.Alerts.ListHistoryBySender(ctx, u.DB, request)
	}
	if err != nil {
		return nil, 0, err
	}

	items, err := u.buildHistoryDetails(ctx, alerts, request.UserID, request.Role)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// Summary aggregates the cards for GET /api/sos/summary. The UI must not
// derive these numbers from the visible page, so they are counted over
// every status.
func (u *TriggerSOSUseCase) Summary(ctx context.Context, userID string) (*model.AlertSummaryResponse, error) {
	sent, received, respondedTotal, respondedResolved, err := u.Deps.Alerts.SummaryCounts(ctx, u.DB, userID)
	if err != nil {
		return nil, err
	}
	return &model.AlertSummaryResponse{
		Sent:      statusCounts(sent),
		Received:  statusCounts(received),
		Responded: model.AlertRespondedCounts{Total: respondedTotal, Resolved: respondedResolved},
	}, nil
}

// statusCounts folds a GROUP BY status result into the response shape.
// Total sums every bucket so an unexpected status cannot silently drop
// rows from the total.
func statusCounts(counts map[string]int64) model.AlertStatusCounts {
	out := model.AlertStatusCounts{
		Active:    counts[entity.AlertStatusTriggered] + counts[entity.AlertStatusAcknowledged],
		Resolved:  counts[entity.AlertStatusResolved],
		Cancelled: counts[entity.AlertStatusCancelled],
		Expired:   counts[entity.AlertStatusExpired],
	}
	for _, n := range counts {
		out.Total += n
	}
	return out
}

// buildHistoryDetails maps a page of alerts to detail responses through
// the shared batched builder (#59): responder counts, latest breadcrumbs,
// the caller's own responses (masking) and sender names are each batched
// once, and the independent batches run concurrently. History never
// carries the responder roster or breadcrumbs.
func (u *TriggerSOSUseCase) buildHistoryDetails(ctx context.Context, alerts []*entity.Alert, requesterID, role string) ([]*model.AlertDetailResponse, error) {
	return u.buildAlertDetails(ctx, alerts, detailBatchOptions{
		requesterID: requesterID,
		role:        role,
	})
}
