package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRateLimitTestRepo(t *testing.T) *RateLimitRepository {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRateLimitRepository(client)
}

// The classify cap must stop the (limit+1)-th paid call inside one window.
func TestRateLimitAllowClassifyDeniesAboveLimit(t *testing.T) {
	repo := newRateLimitTestRepo(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		allowed, err := repo.AllowClassify(ctx, "user-a", 3, time.Hour)
		if err != nil || !allowed {
			t.Fatalf("call %d: allowed = %v err = %v, want true", i+1, allowed, err)
		}
	}
	allowed, err := repo.AllowClassify(ctx, "user-a", 3, time.Hour)
	if err != nil {
		t.Fatalf("AllowClassify error = %v", err)
	}
	if allowed {
		t.Fatal("4th classification allowed, want denied")
	}

	// The trigger budget is a different bucket.
	allowed, err = repo.Allow(ctx, "user-a", 3, time.Hour)
	if err != nil || !allowed {
		t.Fatalf("trigger Allow = %v err = %v, want true (separate key)", allowed, err)
	}
}
