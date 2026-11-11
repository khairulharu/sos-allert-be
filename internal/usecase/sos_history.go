package usecase

import (
	"context"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/gofiber/fiber/v2"
)

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
	sent, err := u.Deps.Alerts.CountBySenderStatus(ctx, u.DB, userID)
	if err != nil {
		return nil, err
	}
	received, err := u.Deps.Alerts.CountByRecipientStatus(ctx, u.DB, userID)
	if err != nil {
		return nil, err
	}
	respondedTotal, respondedResolved, err := u.Deps.Alerts.CountResponded(ctx, u.DB, userID)
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

// buildHistoryDetails maps a page of alerts to detail responses with a
// fixed number of queries (no per-row lookups): responder counts, latest
// breadcrumbs, the caller's own responses (masking) and sender names are
// each batched once.
func (u *TriggerSOSUseCase) buildHistoryDetails(ctx context.Context, alerts []*entity.Alert, requesterID, role string) ([]*model.AlertDetailResponse, error) {
	items := make([]*model.AlertDetailResponse, 0, len(alerts))
	if len(alerts) == 0 {
		return items, nil
	}

	ids := make([]string, 0, len(alerts))
	senderIDs := make([]string, 0, len(alerts))
	seenSender := make(map[string]struct{}, len(alerts))
	for _, alert := range alerts {
		ids = append(ids, alert.ID)
		if _, ok := seenSender[alert.SenderID]; !ok {
			seenSender[alert.SenderID] = struct{}{}
			senderIDs = append(senderIDs, alert.SenderID)
		}
	}

	responderCounts, err := u.Deps.Alerts.CountRespondersByAlertIDs(ctx, u.DB, ids)
	if err != nil {
		return nil, err
	}
	latestCrumbs, err := u.Deps.Alerts.LatestBreadcrumbsByAlertIDs(ctx, u.DB, ids)
	if err != nil {
		return nil, err
	}
	// role=sent means the caller is the sender of every row, so no
	// responder lookup is needed to decide masking there.
	responded := map[string]bool{}
	if role != "sent" {
		if responded, err = u.Deps.Alerts.ListRespondedAlertIDs(ctx, u.DB, requesterID, ids); err != nil {
			return nil, err
		}
	}
	names := map[string]string{}
	if u.Deps.Users != nil && len(senderIDs) > 0 {
		if users, err := u.Deps.Users.ListByIDs(ctx, senderIDs); err != nil {
			u.Log.WithError(err).Warn("could not load history sender names")
		} else {
			for id, user := range users {
				names[id] = user.Name
			}
		}
	}

	for _, alert := range alerts {
		detail := alertDetailBase(alert)
		detail.Sender.Name = names[alert.SenderID]
		detail.RespondersEnRoute = responderCounts[alert.ID]

		initial := model.LocationPoint{Latitude: alert.Latitude, Longitude: alert.Longitude}
		latest := initial
		if crumb := latestCrumbs[alert.ID]; crumb != nil {
			ts := crumb.RecordedAt
			latest = model.LocationPoint{Latitude: crumb.Latitude, Longitude: crumb.Longitude, UpdatedAt: &ts}
		}
		isParticipant := alert.SenderID == requesterID || responded[alert.ID]
		setDetailLocations(detail, initial, latest, isParticipant)
		items = append(items, detail)
	}
	return items, nil
}
