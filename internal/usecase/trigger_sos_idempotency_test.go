package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
)

// bodyCoordinates builds a trigger request carrying explicit coordinates so
// the test does not depend on the per-user Redis location of a second sender.
func bodyCoordinates() *model.TriggerSOSRequest {
	lat, lng := -6.2088, 106.8456
	return &model.TriggerSOSRequest{Latitude: &lat, Longitude: &lng}
}

// A retry from the same user with the same key replays the original alert and
// must not create a second one (#35: the key is scoped, not global).
func TestTriggerSOSIdempotencySameUserReplaysSameAlert(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	idem := &fakeIdempotency{}
	uc.Deps.Idempotency = idem

	first, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "retry-key")
	if err != nil {
		t.Fatalf("first trigger error = %v", err)
	}
	second, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "retry-key")
	if err != nil {
		t.Fatalf("replay error = %v", err)
	}

	if second.Alert.ID != first.Alert.ID {
		t.Fatalf("replay alert id = %s, want %s", second.Alert.ID, first.Alert.ID)
	}
	if len(alertRepo.alerts) != 1 {
		t.Fatalf("alerts created = %d, want 1 (retry must not duplicate)", len(alertRepo.alerts))
	}
	if _, ok := idem.values["sender:retry-key"]; !ok {
		t.Fatalf("stored idempotency keys = %v, want the sender-scoped key", idem.values)
	}
}

// Two different users sending the same X-Idempotency-Key must each get their
// own alert: the second trigger must never be swallowed as a replay of the
// first user's SOS.
func TestTriggerSOSIdempotencyIsScopedPerSender(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	idem := &fakeIdempotency{}
	uc.Deps.Idempotency = idem

	first, err := uc.ExecuteWithRequest(context.Background(), "user-a", bodyCoordinates(), "same-key")
	if err != nil {
		t.Fatalf("user-a trigger error = %v", err)
	}
	second, err := uc.ExecuteWithRequest(context.Background(), "user-b", bodyCoordinates(), "same-key")
	if err != nil {
		t.Fatalf("user-b trigger error = %v", err)
	}

	if first.Alert.ID == second.Alert.ID {
		t.Fatalf("both senders replayed alert %s, want distinct alerts", first.Alert.ID)
	}
	if len(alertRepo.alerts) != 2 {
		t.Fatalf("alerts created = %d, want 2 (one per sender)", len(alertRepo.alerts))
	}
	if second.Alert.SenderId != "user-b" {
		t.Fatalf("user-b result sender = %q, want user-b", second.Alert.SenderId)
	}
	if _, ok := alertRepo.alerts[second.Alert.ID]; !ok {
		t.Fatalf("user-b alert %s was not created", second.Alert.ID)
	}
	// The two write the same client key under distinct scoped keys.
	if idem.values["user-a:same-key"] == "" || idem.values["user-b:same-key"] == "" {
		t.Fatalf("stored idempotency keys = %v, want both sender-scoped entries", idem.values)
	}
}

// foreignIdempotency simulates a compromised/misbehaving store that resolves
// every key to another user's alert id.
type foreignIdempotency struct {
	alertID string
}

func (f *foreignIdempotency) Claim(ctx context.Context, key string) (bool, error) {
	return true, nil
}

func (f *foreignIdempotency) Get(ctx context.Context, key string) (string, error) {
	return f.alertID + "|0", nil
}

func (f *foreignIdempotency) Set(ctx context.Context, key, value string) error {
	return nil
}

func (f *foreignIdempotency) Release(ctx context.Context, key string) error {
	return nil
}

// Defense in depth: even if the store hands back an alert id owned by someone
// else, it must not be replayed and must not appear in the response.
func TestTriggerSOSIdempotencyIgnoresForeignAlert(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	victim := &entity.Alert{
		ID:       "victim-alert-id",
		SenderID: "victim",
		Status:   entity.AlertStatusTriggered,
	}
	if err := alertRepo.Create(context.Background(), nil, victim); err != nil {
		t.Fatalf("seed victim alert: %v", err)
	}
	alertRepo.createCalled = false
	alertRepo.lastAlert = nil
	uc.Deps.Idempotency = &foreignIdempotency{alertID: victim.ID}

	result, err := uc.ExecuteWithRequest(context.Background(), "attacker", bodyCoordinates(), "collide")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	if result.Alert.ID == victim.ID {
		t.Fatalf("result replayed the foreign alert %s", victim.ID)
	}
	if strings.Contains(result.Alert.ID, victim.ID) {
		t.Fatalf("result alert id %q leaks the foreign alert id", result.Alert.ID)
	}
	if !alertRepo.createCalled {
		t.Fatal("a foreign stored id must not stop the caller's own alert from being created")
	}
	if result.Alert.SenderId != "attacker" {
		t.Fatalf("result sender = %q, want attacker", result.Alert.SenderId)
	}
	if _, ok := alertRepo.alerts[result.Alert.ID]; !ok {
		t.Fatalf("alert %s was not created for the caller", result.Alert.ID)
	}
}

// failingIdempotency stands in for a Redis outage.
type failingIdempotency struct{}

func (f *failingIdempotency) Claim(ctx context.Context, key string) (bool, error) {
	return false, errors.New("redis down")
}

func (f *failingIdempotency) Get(ctx context.Context, key string) (string, error) {
	return "", errors.New("redis down")
}

func (f *failingIdempotency) Set(ctx context.Context, key, value string) error {
	return errors.New("redis down")
}

func (f *failingIdempotency) Release(ctx context.Context, key string) error {
	return errors.New("redis down")
}

// Fail-open: an unavailable idempotency store must never block a life-safety
// trigger.
func TestTriggerSOSIdempotencyStoreErrorFailsOpen(t *testing.T) {
	uc, _, alertRepo, notifier := triggerSetup(t)
	uc.Deps.Idempotency = &failingIdempotency{}

	result, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "key-1")
	if err != nil {
		t.Fatalf("idempotency outage should fail open, got %v", err)
	}
	if !alertRepo.createCalled || !notifier.notifyCalled {
		t.Fatal("trigger must create and dispatch when the idempotency store errors")
	}
	if result == nil || result.Alert.ID == "" {
		t.Fatalf("result = %+v, want a created alert", result)
	}
}

// A trigger that dies after the claim but before the alert is stored must hand
// the claim back, so the retry with the same key creates the alert instead of
// being answered 409 for the rest of the 5-minute TTL (#36).
func TestTriggerSOSIdempotencyReleasedWhenAlertCreateFails(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	idem := &fakeIdempotency{}
	uc.Deps.Idempotency = idem

	saveErr := errors.New("db write failed")
	alertRepo.createErr = saveErr
	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "retry-key"); !errors.Is(err, saveErr) {
		t.Fatalf("first trigger error = %v, want %v", err, saveErr)
	}
	if idem.releases != 1 {
		t.Fatalf("Release calls = %d, want 1 after a failed trigger", idem.releases)
	}
	if len(idem.releaseKeys) != 1 || idem.releaseKeys[0] != "sender:retry-key" {
		t.Fatalf("released keys = %v, want the sender-scoped key", idem.releaseKeys)
	}
	if _, held := idem.values["sender:retry-key"]; held {
		t.Fatalf("pending claim still held after the failure: %v", idem.values)
	}

	alertRepo.createErr = nil
	result, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "retry-key")
	if err != nil {
		t.Fatalf("retry error = %v, want the retry to succeed instead of 409", err)
	}
	if result == nil || result.Alert.ID == "" {
		t.Fatalf("retry result = %+v, want a created alert", result)
	}
	if len(alertRepo.alerts) != 1 {
		t.Fatalf("alerts created = %d, want 1 after the successful retry", len(alertRepo.alerts))
	}
	if _, ok := alertRepo.alerts[result.Alert.ID]; !ok {
		t.Fatalf("alert %s from the retry was not created", result.Alert.ID)
	}
	if stored := idem.values["sender:retry-key"]; stored == "" || stored == "pending" {
		t.Fatalf("stored idempotency value = %q, want the retry's alert id", stored)
	}
}

// The recipient fan-out error path happens after the claim too, so it must
// release as well; otherwise the sender cannot retry for five minutes.
func TestTriggerSOSIdempotencyReleasedWhenFanoutFails(t *testing.T) {
	uc, locRepo, _, _ := triggerSetup(t)
	idem := &fakeIdempotency{}
	uc.Deps.Idempotency = idem

	geoErr := errors.New("redis geo down")
	locRepo.nearbyErr = geoErr
	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "fanout-key"); !errors.Is(err, geoErr) {
		t.Fatalf("trigger error = %v, want %v", err, geoErr)
	}
	if idem.releases != 1 {
		t.Fatalf("Release calls = %d, want 1 after a fan-out failure", idem.releases)
	}
	if _, held := idem.values["sender:fanout-key"]; held {
		t.Fatalf("pending claim still held after the fan-out failure: %v", idem.values)
	}
}

// A trigger that stores its result owns the key: releasing it would let a
// retry duplicate the alert.
func TestTriggerSOSIdempotencySuccessDoesNotRelease(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	idem := &fakeIdempotency{}
	uc.Deps.Idempotency = idem

	result, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "done-key")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	if idem.releases != 0 {
		t.Fatalf("Release calls = %d, want 0 after a successful trigger", idem.releases)
	}
	if stored := idem.values["sender:done-key"]; stored != result.Alert.ID+"|3" {
		t.Fatalf("stored idempotency value = %q, want the finished result", stored)
	}
	if len(alertRepo.alerts) != 1 {
		t.Fatalf("alerts created = %d, want 1", len(alertRepo.alerts))
	}
}

// A Release failure is fail-open and must not replace the error that aborted
// the trigger.
func TestTriggerSOSIdempotencyReleaseErrorKeepsOriginalError(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	idem := &fakeIdempotency{releaseErr: errors.New("redis down")}
	uc.Deps.Idempotency = idem

	saveErr := errors.New("db write failed")
	alertRepo.createErr = saveErr
	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "boom-key"); !errors.Is(err, saveErr) {
		t.Fatalf("trigger error = %v, want the original %v", err, saveErr)
	}
	if idem.releases != 1 {
		t.Fatalf("Release calls = %d, want the release to be attempted once", idem.releases)
	}
}
