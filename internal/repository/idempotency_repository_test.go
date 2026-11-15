package repository

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newIdempotencyTestRepo(t *testing.T) (*IdempotencyRepository, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewIdempotencyRepository(client), server
}

// Release hands an unfinished claim back: the key disappears, so the retry of
// a failed trigger can claim it again instead of waiting out the TTL (#36).
func TestIdempotencyReleaseDeletesPendingClaim(t *testing.T) {
	repo, server := newIdempotencyTestRepo(t)
	ctx := context.Background()

	claimed, err := repo.Claim(ctx, "sender:key-1")
	if err != nil {
		t.Fatalf("Claim error = %v", err)
	}
	if !claimed {
		t.Fatal("Claim = false, want the first claim to win")
	}

	if err := repo.Release(ctx, "sender:key-1"); err != nil {
		t.Fatalf("Release error = %v", err)
	}
	if server.Exists(idempotencyKeyPrefix + "sender:key-1") {
		t.Fatal("pending key still exists after Release")
	}

	again, err := repo.Claim(ctx, "sender:key-1")
	if err != nil {
		t.Fatalf("second Claim error = %v", err)
	}
	if !again {
		t.Fatal("second Claim = false, want the released key to be claimable")
	}
}

// A finished trigger owns its key: Release must never delete another
// request's stored result.
func TestIdempotencyReleaseKeepsFinishedResult(t *testing.T) {
	repo, server := newIdempotencyTestRepo(t)
	ctx := context.Background()

	claimed, err := repo.Claim(ctx, "sender:key-2")
	if err != nil || !claimed {
		t.Fatalf("Claim = %v, %v; want true, nil", claimed, err)
	}
	if err := repo.Set(ctx, "sender:key-2", "alert-1|3"); err != nil {
		t.Fatalf("Set error = %v", err)
	}

	if err := repo.Release(ctx, "sender:key-2"); err != nil {
		t.Fatalf("Release error = %v", err)
	}

	got, err := server.Get(idempotencyKeyPrefix + "sender:key-2")
	if err != nil || got != "alert-1|3" {
		t.Fatalf("stored value = %q, %v; want alert-1|3 preserved", got, err)
	}
	value, err := repo.Get(ctx, "sender:key-2")
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if value != "alert-1|3" {
		t.Fatalf("Get = %q, want the finished result", value)
	}
	// The surviving key still blocks a fresh claim, so no duplicate alert.
	if claimed, err := repo.Claim(ctx, "sender:key-2"); err != nil || claimed {
		t.Fatalf("Claim after a finished result = %v, %v; want false, nil", claimed, err)
	}
}

// Releasing a key that was never claimed (or already expired) is a no-op.
func TestIdempotencyReleaseMissingKeyIsNoop(t *testing.T) {
	repo, _ := newIdempotencyTestRepo(t)

	if err := repo.Release(context.Background(), "sender:absent"); err != nil {
		t.Fatalf("Release error = %v", err)
	}
}
