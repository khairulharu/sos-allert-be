package usecase

import (
	"context"
	"errors"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
)

// Durable grace-period outbox (#40).
//
// The fan-out used to be scheduled with time.AfterFunc inside the web
// process. A restart, deploy or OOM inside the grace window then lost it:
// the alert stayed TRIGGERED with its recipients already recorded in
// alert_recipients, and nobody was ever notified. The due time now lives on
// the alerts row (dispatch_due_at) and RunDispatchLoop polls for it, so the
// fan-out survives a process restart.
//
// Semantics are at-least-once. MarkDispatched is a one-way latch, but a
// crash between "sent" and "marked" makes the next tick re-send. A
// duplicate notification is acceptable; a lost SOS is not.
const (
	// dispatchBatchLimit caps one scan so a large backlog cannot hold the
	// loop for minutes.
	dispatchBatchLimit = 50
	// dispatchAlertTimeout bounds one alert's fan-out, so a slow provider
	// cannot stall the rest of the batch.
	dispatchAlertTimeout = 10 * time.Second
	// dispatchMarkTimeout bounds the bookkeeping update when the dispatch
	// context already expired.
	dispatchMarkTimeout = 2 * time.Second
	// minDispatchPollInterval is the floor for sos.dispatch_poll_seconds.
	minDispatchPollInterval = 100 * time.Millisecond
)

// RunDispatchLoop fan-outs alerts whose grace period elapsed. It blocks
// until ctx is cancelled and is meant to run in the web process only (the
// worker has no dispatcher or database wiring).
func (u *TriggerSOSUseCase) RunDispatchLoop(ctx context.Context, interval time.Duration) {
	if interval < minDispatchPollInterval {
		interval = minDispatchPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := u.DispatchDueAlerts(ctx); err != nil {
			u.Log.WithError(err).Warn("grace-period dispatch scan failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// DispatchDueAlerts runs one outbox scan and returns how many alerts were
// fanned out. It never returns an error for a single alert: one bad row
// must not stop the rest of the batch.
func (u *TriggerSOSUseCase) DispatchDueAlerts(ctx context.Context) (int, error) {
	if u.Deps.Alerts == nil {
		return 0, nil
	}
	due, err := u.Deps.Alerts.ListDueDispatch(ctx, u.DB, time.Now().UnixMilli(), dispatchBatchLimit)
	if err != nil {
		return 0, err
	}
	dispatched := 0
	for _, alert := range due {
		if ctx.Err() != nil {
			// Shutdown: leave the remaining rows unstamped so the next
			// process dispatches them instead of silently dropping them.
			break
		}
		if u.dispatchDueAlert(ctx, alert) {
			dispatched++
		}
	}
	return dispatched, nil
}

// dispatchDueAlert fans out one due alert. It reports whether the fan-out
// actually ran; a skipped (cancelled/resolved) alert is marked dispatched so
// the scan stops returning it, but is never sent to anyone.
func (u *TriggerSOSUseCase) dispatchDueAlert(ctx context.Context, due *entity.Alert) bool {
	alertCtx, cancel := context.WithTimeout(ctx, dispatchAlertTimeout)
	defer cancel()

	// The scan result is only a hint: re-read the row so a cancel or
	// resolve that raced the query is never fanned out.
	alert, err := u.Deps.Alerts.GetByID(alertCtx, u.DB, due.ID)
	if err != nil {
		if errors.Is(err, model.ErrAlertNotFound) {
			// Deleted between the scan and now: nothing to dispatch.
			u.markDispatched(alertCtx, due.ID)
			return false
		}
		// Unknown/unreadable state: do not guess. Leave the row unstamped
		// so the next tick retries it.
		u.Log.WithError(err).Errorf("alert %s: could not re-check before dispatch, will retry", due.ID)
		return false
	}
	if alert == nil {
		u.markDispatched(alertCtx, due.ID)
		return false
	}
	if alert.Status != entity.AlertStatusTriggered {
		u.Log.Infof("alert %s is %s, skipping grace-period dispatch", alert.ID, alert.Status)
		u.markDispatched(alertCtx, alert.ID)
		return false
	}

	// Same fail-open rule as the trigger (#37): a recipient lookup error
	// must not swallow the fan-out. Degrade to an empty list so the
	// dispatcher (and its emergency-contact SMS fallback) still runs.
	recipients, err := u.Deps.Alerts.ListRecipients(alertCtx, u.DB, alert.ID)
	degraded := false
	if err != nil {
		u.Log.WithError(err).Errorf("alert %s: recipient lookup failed, dispatching without nearby responders", alert.ID)
		if u.Deps.Metrics != nil {
			u.Deps.Metrics.IncFanoutFailures()
		}
		degraded = true
		recipients = []string{}
	}

	alertModel := alertModelFromEntity(alert)
	severity, summary := 0.0, ""
	if alert.Severity != nil {
		severity = *alert.Severity
	}
	if alert.Summary != nil {
		summary = *alert.Summary
	}
	event := model.SOSEvent{
		EventType:     "sos.dispatched",
		AlertID:       alert.ID,
		SenderID:      alert.SenderID,
		Latitude:      alert.Latitude,
		Longitude:     alert.Longitude,
		EmergencyType: alert.EmergencyType,
		Status:        alert.Status,
		Recipients:    recipients,
		TriggeredAt:   alert.TriggeredAt,
		Severity:      severity,
		Summary:       summary,
	}

	u.sendFanout(alertCtx, event, recipients, alertModel, degraded, time.Now())
	// At-least-once: if this mark fails the next tick re-sends.
	u.markDispatched(alertCtx, alert.ID)
	return true
}

// markDispatched latches dispatched_at. Best effort: a failure only logs,
// because leaving the row unstamped means a retried (duplicate) fan-out,
// which is preferable to a clean-looking alert that reached nobody.
func (u *TriggerSOSUseCase) markDispatched(ctx context.Context, alertID string) {
	if ctx.Err() != nil {
		// The dispatch already used its budget; do not lose the mark to
		// the same deadline.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), dispatchMarkTimeout)
		defer cancel()
	}
	marked, err := u.Deps.Alerts.MarkDispatched(ctx, u.DB, alertID, time.Now().UnixMilli())
	if err != nil {
		u.Log.WithError(err).Errorf("alert %s: could not mark as dispatched, a retry may re-send", alertID)
		return
	}
	if !marked {
		u.Log.Debugf("alert %s was already marked as dispatched", alertID)
	}
}

// alertModelFromEntity rebuilds the notification payload from the stored
// row, so the outbox path notifies with the same content as the inline one
// (the request body is long gone by the time the grace period elapses).
func alertModelFromEntity(alert *entity.Alert) model.Alert {
	out := model.Alert{
		ID:                alert.ID,
		SenderId:          alert.SenderID,
		TriggeredAt:       time.UnixMilli(alert.TriggeredAt),
		Status:            alert.Status,
		EmergencyType:     alert.EmergencyType,
		AccuracyMeters:    alert.AccuracyMeters,
		BatteryPercentage: alert.BatteryPercentage,
		Location: model.LocationResponse{
			Longitude: alert.Longitude,
			Latitude:  alert.Latitude,
		},
	}
	if alert.Severity != nil {
		out.Severity = *alert.Severity
	}
	if alert.Summary != nil {
		out.Summary = *alert.Summary
	}
	return out
}
