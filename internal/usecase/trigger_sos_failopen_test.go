package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
)

// fanoutMetrics counts the fail-open fan-out signal (#37).
type fanoutMetrics struct {
	mu       sync.Mutex
	failures int
}

func (m *fanoutMetrics) ObserveDispatchDuration(float64) {}
func (m *fanoutMetrics) IncPushFailures()                {}
func (m *fanoutMetrics) SetActiveSOS(int64)              {}

func (m *fanoutMetrics) IncFanoutFailures() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures++
}

func (m *fanoutMetrics) value() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failures
}

// Core regression for #37: the alert row is persisted before the fan-out, so
// a Redis GEO error must not abort the trigger. The alert stays, the dispatch
// still runs with an empty recipient list — which is what carries the
// sender's emergency-contact SMS fallback — and the caller gets a result
// instead of a 5xx.
func TestTriggerSOSGeoSearchErrorStillDispatches(t *testing.T) {
	uc, locRepo, alertRepo, notifier := triggerSetup(t)
	metrics := &fanoutMetrics{}
	uc.Deps.Metrics = metrics
	locRepo.nearbyErr = errors.New("redis GEOSEARCH unavailable")

	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("a recipient lookup failure must fail open, got error %v", err)
	}
	if result == nil {
		t.Fatal("result = nil, want the created alert")
	}
	if !alertRepo.createCalled {
		t.Fatal("the alert must be persisted before the fan-out")
	}
	stored, ok := alertRepo.alerts[result.Alert.ID]
	if !ok {
		t.Fatalf("alert %s was not persisted", result.Alert.ID)
	}
	if stored.Status != entity.AlertStatusTriggered {
		t.Fatalf("alert status = %s, want TRIGGERED", stored.Status)
	}
	if notifier.notifyCalls != 1 {
		t.Fatalf("Notify calls = %d, want exactly 1 (fail-open dispatch)", notifier.notifyCalls)
	}
	if len(notifier.lastRecipients) != 0 {
		t.Fatalf("notified recipients = %v, want none", notifier.lastRecipients)
	}
	if notifier.lastAlert.ID != result.Alert.ID {
		t.Fatalf("notified alert = %q, want %q", notifier.lastAlert.ID, result.Alert.ID)
	}
	if len(result.Recipients) != 0 {
		t.Fatalf("result recipients = %v, want none", result.Recipients)
	}
	if result.RadiusMeters != 5000 {
		t.Fatalf("radius = %v, want the widest configured radius (5000) instead of 0", result.RadiusMeters)
	}
	if got := len(alertRepo.recipients[result.Alert.ID]); got != 0 {
		t.Fatalf("recorded recipients = %d, want 0", got)
	}
	if metrics.value() != 1 {
		t.Fatalf("fan-out failure metric = %d, want 1", metrics.value())
	}
}

// A last_seen batch failure keeps the existing degrade: the candidates whose
// freshness is unknown are dropped, but the trigger still completes and
// dispatches (it is not a fan-out failure).
func TestTriggerSOSLastSeenBatchErrorDegrades(t *testing.T) {
	uc, locRepo, _, notifier := triggerSetup(t)
	metrics := &fanoutMetrics{}
	uc.Deps.Metrics = metrics
	locRepo.lastSeenBatchErr = errors.New("redis HMGET unavailable")

	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("last_seen batch failure must not abort the trigger, got %v", err)
	}
	if len(result.Recipients) != 0 {
		t.Fatalf("recipients = %v, want none when freshness is unknown", result.Recipients)
	}
	if notifier.notifyCalls != 1 {
		t.Fatalf("Notify calls = %d, want 1", notifier.notifyCalls)
	}
	if metrics.value() != 0 {
		t.Fatalf("fan-out failure metric = %d, want 0 (the lookup itself succeeded)", metrics.value())
	}
}

// The happy path is untouched: recipients are found, returned, and notified.
func TestTriggerSOSSuccessfulLookupStillNotifiesRecipients(t *testing.T) {
	uc, _, _, notifier := triggerSetup(t)
	metrics := &fanoutMetrics{}
	uc.Deps.Metrics = metrics

	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	want := []string{"user-b", "user-c", "user-d"}
	if len(result.Recipients) != len(want) {
		t.Fatalf("recipients = %v, want %v", result.Recipients, want)
	}
	for i := range want {
		if result.Recipients[i] != want[i] {
			t.Fatalf("recipients = %v, want %v", result.Recipients, want)
		}
	}
	if result.RadiusMeters != 1000 {
		t.Fatalf("radius = %v, want 1000", result.RadiusMeters)
	}
	if notifier.notifyCalls != 1 || len(notifier.lastRecipients) != len(want) {
		t.Fatalf("notified = %d call(s) with %v, want 1 call with %v",
			notifier.notifyCalls, notifier.lastRecipients, want)
	}
	if metrics.value() != 0 {
		t.Fatalf("fan-out failure metric = %d, want 0", metrics.value())
	}
}

// With Kafka enabled, a published event normally replaces the direct notify.
// After a failed lookup there is no responder in the event and the worker has
// no contact resolver, so the local dispatcher must still run to keep the
// emergency-contact SMS fallback (#37).
func TestTriggerSOSGeoSearchErrorStillReachesContactsWithKafka(t *testing.T) {
	uc, locRepo, _, notifier := triggerSetup(t)
	pub := &fakePublisher{}
	uc.Deps.Publisher = pub
	locRepo.nearbyErr = errors.New("redis GEOSEARCH unavailable")

	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, ""); err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	if len(pub.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(pub.events))
	}
	if len(pub.events[0].Recipients) != 0 {
		t.Fatalf("published recipients = %v, want none", pub.events[0].Recipients)
	}
	if notifier.notifyCalls != 1 {
		t.Fatalf("Notify calls = %d, want 1 so the contact fallback still runs", notifier.notifyCalls)
	}
	if len(notifier.lastRecipients) != 0 {
		t.Fatalf("notified recipients = %v, want none", notifier.lastRecipients)
	}
}

// The deferred (grace-period) dispatch must still run after a failed lookup:
// the durable outbox reloads the alert, finds no recorded recipient and
// notifies with the empty list so the contact fallback still runs (#37/#40).
func TestTriggerSOSGracePeriodDispatchWithEmptyFanout(t *testing.T) {
	uc, locRepo, alertRepo, notifier := triggerSetup(t)
	locRepo.nearbyErr = errors.New("redis GEOSEARCH unavailable")
	uc.Deps.GracePeriod = 20 * time.Millisecond

	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	if notifier.notifyCalls != 0 {
		t.Fatalf("Notify calls = %d, want 0 before the grace period elapses", notifier.notifyCalls)
	}

	// Rewind the recorded due time instead of sleeping: the scan only
	// compares dispatch_due_at with the current clock.
	past := time.Now().Add(-time.Second).UnixMilli()
	alertRepo.alerts[result.Alert.ID].DispatchDueAt = &past

	if n, err := uc.DispatchDueAlerts(context.Background()); err != nil || n != 1 {
		t.Fatalf("DispatchDueAlerts = %d, %v; want 1 dispatched", n, err)
	}
	if notifier.notifyCalls != 1 {
		t.Fatalf("Notify calls = %d, want exactly 1", notifier.notifyCalls)
	}
	if len(notifier.lastRecipients) != 0 {
		t.Fatalf("notified recipients = %v, want none", notifier.lastRecipients)
	}
	if notifier.lastAlert.ID != result.Alert.ID {
		t.Fatalf("notified alert = %q, want %q", notifier.lastAlert.ID, result.Alert.ID)
	}
}
