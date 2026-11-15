package usecase

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"golang-clean-architecture/internal/entity"

	"gorm.io/gorm"
)

// Housekeeping (#42) exists only because ExpireOldAlerts/PurgeOldBreadcrumbs
// had no caller: one pass must run both statements and refresh the gauge when
// alerts actually expired.
func TestHousekeepingOnceExpiresPurgesAndRefreshesGauge(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	uc.Deps.ExpireAfter = 24 * time.Hour
	alertRepo.expired = 2
	alertRepo.purged = 7
	metrics := &gaugeMetrics{}
	uc.Deps.Metrics = metrics
	// No active alerts in the store, so the refreshed gauge must report 0.
	alertRepo.alerts = map[string]*entity.Alert{}

	before := time.Now()
	uc.runHousekeepingOnce(context.Background())

	if alertRepo.expireCalls != 1 {
		t.Fatalf("expire calls = %d, want 1", alertRepo.expireCalls)
	}
	if alertRepo.purgeCalls != 1 {
		t.Fatalf("purge calls = %d, want 1", alertRepo.purgeCalls)
	}
	// Expiry cutoff must be now - ExpireAfter, purge cutoff now - 30 days.
	wantExpire := before.Add(-24 * time.Hour).UnixMilli()
	if diff := alertRepo.expireBeforeMs - wantExpire; diff < -5000 || diff > 5000 {
		t.Fatalf("expire cutoff = %d, want ~%d (now - ExpireAfter)", alertRepo.expireBeforeMs, wantExpire)
	}
	wantPurge := before.AddDate(0, 0, -30).UnixMilli()
	if diff := alertRepo.purgeBeforeMs - wantPurge; diff < -5000 || diff > 5000 {
		t.Fatalf("purge cutoff = %d, want ~%d (now - 30 days)", alertRepo.purgeBeforeMs, wantPurge)
	}
	if got := metrics.value(); got != 0 {
		t.Fatalf("gauge = %d, want 0 after the expiry pass", got)
	}
}

// A failing statement must not stop the other one, and the loop keeps going.
func TestHousekeepingOnceToleratesErrors(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	alertRepo.expireErr = errors.New("db down")
	alertRepo.purged = 3
	metrics := &gaugeMetrics{}
	uc.Deps.Metrics = metrics

	uc.runHousekeepingOnce(context.Background())

	if alertRepo.expireCalls != 1 || alertRepo.purgeCalls != 1 {
		t.Fatalf("calls = %d/%d, want 1/1 (an expiry error must not skip the purge)",
			alertRepo.expireCalls, alertRepo.purgeCalls)
	}
	if got := metrics.callCount(); got != 0 {
		t.Fatalf("gauge refreshed %d times after a failed expiry, want 0", got)
	}
}

// housekeepingStore counts the two housekeeping calls with atomics, so the
// loop can run on its own goroutine while the test observes it.
type housekeepingStore struct {
	AlertStore
	expireCalls atomic.Int32
	purgeCalls  atomic.Int32
}

func (s *housekeepingStore) ExpireOlderThan(ctx context.Context, db *gorm.DB, beforeMs int64, expiredAtMs int64) (int64, error) {
	s.expireCalls.Add(1)
	return 0, nil
}

func (s *housekeepingStore) PurgeBreadcrumbs(ctx context.Context, db *gorm.DB, beforeMs int64) (int64, error) {
	s.purgeCalls.Add(1)
	return 0, nil
}

// RunHousekeeping runs one pass immediately and returns promptly when the
// process context is cancelled, so a deploy drain is not delayed.
func TestRunHousekeepingRunsOnceAndStopsOnCancel(t *testing.T) {
	store := &housekeepingStore{}
	uc := newUseCaseWithDeps(TriggerSOSDeps{Alerts: store})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		uc.RunHousekeeping(ctx, 5*time.Millisecond)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.expireCalls.Load() > 0 && store.purgeCalls.Load() > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if store.expireCalls.Load() == 0 || store.purgeCalls.Load() == 0 {
		t.Fatalf("calls = %d/%d, want both before the first wait",
			store.expireCalls.Load(), store.purgeCalls.Load())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunHousekeeping did not return after the context was cancelled")
	}
}

// A store without the AlertStore dependency (misconfiguration) must not panic.
func TestHousekeepingWithoutStoreIsNoop(t *testing.T) {
	uc := newUseCaseWithDeps(TriggerSOSDeps{})
	uc.runHousekeepingOnce(context.Background())
}
