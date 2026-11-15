package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang-clean-architecture/internal/model"
)

// quotaRateLimiter mimics the sliding-window limiter: the first `allow`
// calls pass, every later one is a quota hit.
type quotaRateLimiter struct {
	allow int
	calls int
}

func (f *quotaRateLimiter) Allow(ctx context.Context, userID string, limit int, window time.Duration) (bool, error) {
	f.calls++
	return f.calls <= f.allow, nil
}

// A retry reusing an X-Idempotency-Key is not a new trigger, so it must not
// consume the per-user quota (#42). With the default 3/hour limit the fourth
// retry used to be answered 429 instead of replaying the original alert.
func TestIdempotentReplayDoesNotConsumeRateLimit(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	uc.Deps.Idempotency = &fakeIdempotency{}
	limiter := &quotaRateLimiter{allow: 1}
	uc.Deps.RateLimiter = limiter
	uc.Deps.RateLimitPerHour = 3

	first, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "retry-key")
	if err != nil {
		t.Fatalf("first trigger error = %v", err)
	}
	for i := 0; i < 4; i++ {
		replay, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "retry-key")
		if err != nil {
			t.Fatalf("retry %d error = %v, want a replay instead of a quota hit", i+1, err)
		}
		if replay.Alert.ID != first.Alert.ID {
			t.Fatalf("retry %d replayed alert %s, want %s", i+1, replay.Alert.ID, first.Alert.ID)
		}
	}
	if limiter.calls != 1 {
		t.Fatalf("limiter calls = %d, want exactly 1 (only the new trigger counts)", limiter.calls)
	}
	if len(alertRepo.alerts) != 1 {
		t.Fatalf("alerts created = %d, want 1", len(alertRepo.alerts))
	}
}

// The exemption is limited to replays: a trigger with a fresh key (or none) is
// still rate limited, so a client cannot escape the limiter with new keys.
func TestNewTriggerStillCountsAgainstRateLimit(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	uc.Deps.Idempotency = &fakeIdempotency{}
	limiter := &quotaRateLimiter{allow: 1}
	uc.Deps.RateLimiter = limiter

	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "key-1"); err != nil {
		t.Fatalf("first trigger error = %v", err)
	}
	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "key-2"); !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("second key error = %v, want ErrRateLimited", err)
	}
	// Without a key there is nothing to replay, so the limiter applies too.
	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), ""); !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("keyless trigger error = %v, want ErrRateLimited", err)
	}
	if limiter.calls != 3 {
		t.Fatalf("limiter calls = %d, want 3 (every new trigger is checked, replays are not)", limiter.calls)
	}
}

// A limiter outage stays fail-open, and the idempotent retry keeps replaying.
func TestIdempotentReplaySurvivesRateLimiterOutage(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	uc.Deps.Idempotency = &fakeIdempotency{}
	uc.Deps.RateLimiter = &quotaRateLimiter{allow: 0}

	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "key-1"); err == nil {
		t.Fatal("a new trigger must still be limited")
	}

	uc.Deps.RateLimiter = &fakeRateLimiter{allow: true}
	first, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "key-1")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	uc.Deps.RateLimiter = &fakeRateLimiter{err: errors.New("redis down")}
	replay, err := uc.ExecuteWithRequest(context.Background(), "sender", bodyCoordinates(), "key-1")
	if err != nil {
		t.Fatalf("replay with limiter down error = %v", err)
	}
	if replay.Alert.ID != first.Alert.ID {
		t.Fatalf("replay alert = %s, want %s", replay.Alert.ID, first.Alert.ID)
	}
}
