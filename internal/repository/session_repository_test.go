package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newSessionTestRepo(t *testing.T) (*SessionRepository, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewSessionRepository(client), server
}

func TestSessionCreateStoresUserAndTTL(t *testing.T) {
	repo, server := newSessionTestRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, "user-1", "token-1", 168*time.Hour); err != nil {
		t.Fatalf("Create error = %v", err)
	}

	userID, err := repo.Get(ctx, "token-1")
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if userID != "user-1" {
		t.Fatalf("Get = %q, want user-1", userID)
	}

	if got := server.TTL(authTokenKeyPrefix + "token-1"); got != 168*time.Hour {
		t.Errorf("token TTL = %v, want 168h", got)
	}
	if got := server.TTL(authUserKeyPrefix + "user-1"); got != 168*time.Hour {
		t.Errorf("user TTL = %v, want 168h", got)
	}
}

func TestSessionGetUnknownTokenIsEmpty(t *testing.T) {
	repo, _ := newSessionTestRepo(t)

	userID, err := repo.Get(context.Background(), "missing")
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if userID != "" {
		t.Fatalf("Get = %q, want empty", userID)
	}
}

func TestSessionLoginReplacesPreviousToken(t *testing.T) {
	repo, server := newSessionTestRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, "user-1", "token-1", time.Hour); err != nil {
		t.Fatalf("first Create error = %v", err)
	}
	if err := repo.Create(ctx, "user-1", "token-2", time.Hour); err != nil {
		t.Fatalf("second Create error = %v", err)
	}

	if userID, _ := repo.Get(ctx, "token-1"); userID != "" {
		t.Errorf("old token still valid for %q", userID)
	}
	if userID, _ := repo.Get(ctx, "token-2"); userID != "user-1" {
		t.Errorf("new token resolves to %q, want user-1", userID)
	}
	if got, err := server.Get(authUserKeyPrefix + "user-1"); err != nil || got != "token-2" {
		t.Errorf("reverse mapping = %q, %v; want token-2", got, err)
	}
}

func TestSessionDeleteByUserRemovesBothKeys(t *testing.T) {
	repo, server := newSessionTestRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, "user-1", "token-1", time.Hour); err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if err := repo.DeleteByUser(ctx, "user-1"); err != nil {
		t.Fatalf("DeleteByUser error = %v", err)
	}

	if server.Exists(authTokenKeyPrefix + "token-1") {
		t.Error("token key still exists after logout")
	}
	if server.Exists(authUserKeyPrefix + "user-1") {
		t.Error("user key still exists after logout")
	}
	if userID, _ := repo.Get(ctx, "token-1"); userID != "" {
		t.Errorf("token still resolves to %q after logout", userID)
	}
}

func TestSessionExpiresAfterTTL(t *testing.T) {
	repo, server := newSessionTestRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, "user-1", "token-1", time.Hour); err != nil {
		t.Fatalf("Create error = %v", err)
	}
	server.FastForward(2 * time.Hour)

	if userID, err := repo.Get(ctx, "token-1"); err != nil || userID != "" {
		t.Errorf("Get after expiry = %q, %v; want empty, nil", userID, err)
	}
	if server.Exists(authUserKeyPrefix + "user-1") {
		t.Error("user key outlived the token")
	}
}

func TestSessionCreateWithoutTTLKeepsKeys(t *testing.T) {
	repo, server := newSessionTestRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, "user-1", "token-1", 0); err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if got := server.TTL(authTokenKeyPrefix + "token-1"); got != 0 {
		t.Errorf("token TTL = %v, want no expiry", got)
	}
}
