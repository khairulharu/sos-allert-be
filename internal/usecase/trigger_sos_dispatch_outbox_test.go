package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
)

// Regression tests for #40: the post-grace fan-out used to be a
// time.AfterFunc inside the web process, so a restart/deploy/OOM inside the
// grace window left the alert TRIGGERED with its recipients recorded and
// notified nobody. The due time now lives on the alert row and the scan
// dispatches it.

// triggerWithGrace records an alert that has a grace period and returns the
// use case, the store and the notifier. Nothing must be notified inline.
func triggerWithGrace(t *testing.T, grace time.Duration) (*TriggerSOSUseCase, *fakeAlertRepository, *fakeNotifier, *model.TriggerSOSResult) {
	t.Helper()
	uc, _, alertRepo, notifier := triggerSetup(t)
	uc.Deps.GracePeriod = grace
	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	return uc, alertRepo, notifier, result
}

// rewindDue moves the recorded due time into the past so a scan dispatches
// immediately, instead of sleeping through a real grace period.
func rewindDue(t *testing.T, alertRepo *fakeAlertRepository, alertID string) {
	t.Helper()
	past := time.Now().Add(-time.Second).UnixMilli()
	alertRepo.ensure()
	alert, ok := alertRepo.alerts[alertID]
	if !ok {
		t.Fatalf("alert %s not persisted", alertID)
	}
	alert.DispatchDueAt = &past
}

// A grace period must persist the due time with the alert and dispatch
// nothing inline: the recipients are recorded, the notification waits.
func TestTriggerSOSGracePeriodPersistsDispatchDue(t *testing.T) {
	uc, alertRepo, notifier, result := triggerWithGrace(t, 10*time.Second)

	alert, ok := alertRepo.alerts[result.Alert.ID]
	if !ok {
		t.Fatalf("alert %s not persisted", result.Alert.ID)
	}
	if alert.DispatchDueAt == nil {
		t.Fatal("dispatch_due_at = nil, want the due time persisted for the outbox")
	}
	want := alert.TriggeredAt + 10_000
	if *alert.DispatchDueAt != want {
		t.Fatalf("dispatch_due_at = %d, want %d (triggered_at + grace)", *alert.DispatchDueAt, want)
	}
	if alert.DispatchedAt != nil {
		t.Fatalf("dispatched_at = %d, want nil before the grace period elapses", *alert.DispatchedAt)
	}
	if notifier.notifyCalls != 0 {
		t.Fatalf("Notify calls = %d, want 0 (nothing may be sent during the grace period)", notifier.notifyCalls)
	}
	if got := len(alertRepo.recipients[result.Alert.ID]); got == 0 {
		t.Fatal("recipients must be recorded before the grace period ends")
	}
	if result.GracePeriodSeconds != 10 {
		t.Fatalf("grace period seconds = %d, want 10", result.GracePeriodSeconds)
	}

	// A scan before the due time is a no-op.
	if n, err := uc.DispatchDueAlerts(context.Background()); err != nil || n != 0 {
		t.Fatalf("DispatchDueAlerts = %d, %v; want 0 so close to the trigger", n, err)
	}
	if notifier.notifyCalls != 0 {
		t.Fatalf("Notify calls = %d, want 0 for an alert whose due time is in the future", notifier.notifyCalls)
	}
}

// The scan fans out a due alert exactly once and latches dispatched_at, so
// the next tick cannot send it twice.
func TestDispatchDueAlertSendsOnceAndMarks(t *testing.T) {
	uc, alertRepo, notifier, result := triggerWithGrace(t, 10*time.Second)
	rewindDue(t, alertRepo, result.Alert.ID)

	if n, err := uc.DispatchDueAlerts(context.Background()); err != nil || n != 1 {
		t.Fatalf("first scan = %d, %v; want 1 dispatched", n, err)
	}
	if notifier.notifyCalls != 1 {
		t.Fatalf("Notify calls = %d, want exactly 1", notifier.notifyCalls)
	}
	if len(notifier.lastRecipients) != 3 {
		t.Fatalf("notified recipients = %v, want the recorded 3", notifier.lastRecipients)
	}
	if notifier.lastAlert.ID != result.Alert.ID {
		t.Fatalf("notified alert = %q, want %q", notifier.lastAlert.ID, result.Alert.ID)
	}
	if notifier.lastAlert.SenderId != "sender" {
		t.Fatalf("notified sender = %q, want sender", notifier.lastAlert.SenderId)
	}
	if notifier.lastAlert.TriggeredAt.UnixMilli() != result.Alert.TriggeredAt.UnixMilli() {
		t.Fatalf("notified triggered_at = %v, want %v", notifier.lastAlert.TriggeredAt, result.Alert.TriggeredAt)
	}
	alert := alertRepo.alerts[result.Alert.ID]
	if alert.DispatchedAt == nil {
		t.Fatal("dispatched_at = nil, want the alert marked as dispatched")
	}

	// A second tick must not re-send.
	if n, err := uc.DispatchDueAlerts(context.Background()); err != nil || n != 0 {
		t.Fatalf("second scan = %d, %v; want 0 dispatched", n, err)
	}
	if notifier.notifyCalls != 1 {
		t.Fatalf("Notify calls after the second tick = %d, want still 1", notifier.notifyCalls)
	}
}

// A cancelled false alarm is marked so the scan stops returning it, but is
// never fanned out. The cancel lands between the scan and the dispatch,
// which is the only window where the code path can see a cancelled row.
func TestDispatchDueAlertSkipsCancelled(t *testing.T) {
	uc, alertRepo, notifier, result := triggerWithGrace(t, 10*time.Second)
	rewindDue(t, alertRepo, result.Alert.ID)
	alertRepo.afterListDue = func() {
		changed, err := alertRepo.Transition(context.Background(), nil, result.Alert.ID,
			[]string{entity.AlertStatusTriggered},
			map[string]any{"status": entity.AlertStatusCancelled})
		if err != nil || !changed {
			t.Errorf("cancel transition = %v, %v", changed, err)
		}
	}

	if n, err := uc.DispatchDueAlerts(context.Background()); err != nil || n != 0 {
		t.Fatalf("scan = %d, %v; want 0 dispatched for a cancelled alert", n, err)
	}
	if notifier.notifyCalls != 0 {
		t.Fatalf("Notify calls = %d, want 0 for a cancelled alert", notifier.notifyCalls)
	}
	if alertRepo.alerts[result.Alert.ID].DispatchedAt == nil {
		t.Fatal("a skipped cancelled alert must still be marked so the scan stops returning it")
	}
	alertRepo.afterListDue = nil
	if n, err := uc.DispatchDueAlerts(context.Background()); err != nil || n != 0 {
		t.Fatalf("second scan = %d, %v; want 0", n, err)
	}
}

// The scan ignores alerts whose due time has not arrived yet.
func TestDispatchDueAlertIgnoresFutureDueTime(t *testing.T) {
	uc, alertRepo, notifier, result := triggerWithGrace(t, 10*time.Second)
	future := time.Now().Add(time.Hour).UnixMilli()
	alertRepo.alerts[result.Alert.ID].DispatchDueAt = &future

	if n, err := uc.DispatchDueAlerts(context.Background()); err != nil || n != 0 {
		t.Fatalf("scan = %d, %v; want 0 dispatched", n, err)
	}
	if notifier.notifyCalls != 0 {
		t.Fatalf("Notify calls = %d, want 0", notifier.notifyCalls)
	}
	if alertRepo.alerts[result.Alert.ID].DispatchedAt != nil {
		t.Fatal("a not-yet-due alert must stay unmarked")
	}
}

// The regression for the lost fan-out: the process dies inside the grace
// window and a fresh use case (new process) over the same store dispatches
// the alert from the persisted due time alone.
func TestDispatchDueAlertSurvivesRestart(t *testing.T) {
	_, alertRepo, notifier, result := triggerWithGrace(t, 10*time.Second)
	if notifier.notifyCalls != 0 {
		t.Fatalf("the first process must not notify during the grace period")
	}
	// The "restart": a brand-new use case over the same durable store,
	// with the original request context long gone.
	restartedNotifier := &fakeNotifier{}
	restarted := newUseCase(&fakeLocationRepository{}, alertRepo, restartedNotifier)
	rewindDue(t, alertRepo, result.Alert.ID)

	if n, err := restarted.DispatchDueAlerts(context.Background()); err != nil || n != 1 {
		t.Fatalf("post-restart scan = %d, %v; want 1 dispatched", n, err)
	}
	if restartedNotifier.notifyCalls != 1 {
		t.Fatalf("post-restart Notify calls = %d, want 1 (the fan-out must survive the restart)", restartedNotifier.notifyCalls)
	}
	if restartedNotifier.lastAlert.ID != result.Alert.ID {
		t.Fatalf("post-restart notified alert = %q, want %q", restartedNotifier.lastAlert.ID, result.Alert.ID)
	}
	if alertRepo.alerts[result.Alert.ID].DispatchedAt == nil {
		t.Fatal("the restarted process must mark the alert as dispatched")
	}
}

// With Kafka configured the outbox path publishes the same event and still
// marks the alert, so the worker owns delivery.
func TestDispatchDueAlertPublishesToKafkaAndMarks(t *testing.T) {
	uc, alertRepo, notifier, result := triggerWithGrace(t, 10*time.Second)
	pub := &fakePublisher{}
	uc.Deps.Publisher = pub
	rewindDue(t, alertRepo, result.Alert.ID)

	if n, err := uc.DispatchDueAlerts(context.Background()); err != nil || n != 1 {
		t.Fatalf("scan = %d, %v; want 1 dispatched", n, err)
	}
	if len(pub.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(pub.events))
	}
	event := pub.events[0]
	if event.AlertID != result.Alert.ID || event.EventType != "sos.dispatched" {
		t.Fatalf("event = %+v, want sos.dispatched for %s", event, result.Alert.ID)
	}
	if len(event.Recipients) != 3 || event.SenderID != "sender" {
		t.Fatalf("event recipients/sender = %v/%q, want the 3 recorded recipients of sender",
			event.Recipients, event.SenderID)
	}
	if notifier.notifyCalls != 0 {
		t.Fatalf("Notify calls = %d, want 0 when Kafka accepted the event", notifier.notifyCalls)
	}
	if alertRepo.alerts[result.Alert.ID].DispatchedAt == nil {
		t.Fatal("the alert must be marked after a successful publish")
	}
}

// Fail-open (#37) survives the outbox path: a recipient lookup error still
// reaches the notifier (and therefore the emergency-contact fallback).
func TestDispatchDueAlertRecipientLookupFailureStillNotifies(t *testing.T) {
	uc, alertRepo, notifier, result := triggerWithGrace(t, 10*time.Second)
	rewindDue(t, alertRepo, result.Alert.ID)
	alertRepo.recipientsErr = errors.New("mysql down")
	metrics := &fanoutMetrics{}
	uc.Deps.Metrics = metrics

	if n, err := uc.DispatchDueAlerts(context.Background()); err != nil || n != 1 {
		t.Fatalf("scan = %d, %v; want the fan-out to continue", n, err)
	}
	if notifier.notifyCalls != 1 {
		t.Fatalf("Notify calls = %d, want 1 so the contact fallback still runs", notifier.notifyCalls)
	}
	if len(notifier.lastRecipients) != 0 {
		t.Fatalf("notified recipients = %v, want none after a failed lookup", notifier.lastRecipients)
	}
	if metrics.value() != 1 {
		t.Fatalf("fan-out failure metric = %d, want 1", metrics.value())
	}
	if alertRepo.alerts[result.Alert.ID].DispatchedAt == nil {
		t.Fatal("the alert must be marked even after a degraded fan-out")
	}
}

// The blocking loop dispatches on its first pass and stops on ctx cancel.
func TestRunDispatchLoopDispatchesAndStops(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	notifier := &fakeNotifier{notifiedCh: make(chan struct{})}
	uc.Deps.Notifier = notifier

	due := time.Now().Add(-time.Second).UnixMilli()
	if err := alertRepo.Create(context.Background(), nil, &entity.Alert{
		ID:            "alert-due",
		SenderID:      "sender",
		Status:        entity.AlertStatusTriggered,
		TriggeredAt:   due - 10_000,
		DispatchDueAt: &due,
		Latitude:      -6.2088,
		Longitude:     106.8456,
	}); err != nil {
		t.Fatalf("Create error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		uc.RunDispatchLoop(ctx, 10*time.Millisecond)
		close(done)
	}()

	select {
	case <-notifier.notifiedCh:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("RunDispatchLoop never dispatched the due alert")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunDispatchLoop did not stop after ctx was cancelled")
	}
	if notifier.notifyCalls != 1 {
		t.Fatalf("Notify calls = %d, want exactly 1", notifier.notifyCalls)
	}
	if alertRepo.alerts["alert-due"].DispatchedAt == nil {
		t.Fatal("the loop must mark the alert as dispatched")
	}
}
