package usecase

import (
	"context"
	"errors"
	"testing"

	"golang-clean-architecture/internal/model"
)

// A first-time user with no heartbeat and no body coordinates cannot be
// fanned out from: the trigger must fail with the documented
// ErrUserLocationNotFound (mapped to 422), not a wrapped storage error that
// surfaces as a 500, and it must not create an alert (#42).
func TestTriggerSOSWithoutLocationReturnsNotFound(t *testing.T) {
	uc, locRepo, alertRepo, notifier := triggerSetup(t)
	locRepo.senderErr = model.ErrUserLocationNotFound

	_, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if !errors.Is(err, model.ErrUserLocationNotFound) {
		t.Fatalf("error = %v, want ErrUserLocationNotFound", err)
	}
	if alertRepo.createCalled {
		t.Fatal("an alert must not be persisted without coordinates")
	}
	if notifier.notifyCalls != 0 {
		t.Fatalf("notify calls = %d, want 0", notifier.notifyCalls)
	}
}

// A store that returns (nil, nil) is the same case and must not panic.
func TestTriggerSOSWithoutLocationRecordReturnsNotFound(t *testing.T) {
	uc, locRepo, alertRepo, _ := triggerSetup(t)
	locRepo.sender = nil
	locRepo.senderErr = nil

	_, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if !errors.Is(err, model.ErrUserLocationNotFound) {
		t.Fatalf("error = %v, want ErrUserLocationNotFound", err)
	}
	if alertRepo.createCalled {
		t.Fatal("an alert must not be persisted without coordinates")
	}
}

// A missing location store is a misconfiguration, but it must degrade to the
// same 422 instead of a nil-pointer panic.
func TestTriggerSOSWithoutLocationStoreReturnsNotFound(t *testing.T) {
	uc := newUseCaseWithDeps(TriggerSOSDeps{Alerts: &fakeAlertRepository{}})
	_, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if !errors.Is(err, model.ErrUserLocationNotFound) {
		t.Fatalf("error = %v, want ErrUserLocationNotFound", err)
	}
}

// Body coordinates keep working without any stored heartbeat.
func TestTriggerSOSWithBodyCoordinatesNeedsNoHeartbeat(t *testing.T) {
	uc, locRepo, alertRepo, _ := triggerSetup(t)
	locRepo.senderErr = model.ErrUserLocationNotFound

	result, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "")
	if err != nil {
		t.Fatalf("trigger with body coordinates error = %v", err)
	}
	if result == nil || !alertRepo.createCalled {
		t.Fatal("the alert must be created from the body coordinates")
	}
	if len(locRepo.saved) != 1 || locRepo.saved[0].Latitude != *bodyCoordinates().Latitude {
		t.Fatalf("saved locations = %+v, want the body coordinates", locRepo.saved)
	}
}
