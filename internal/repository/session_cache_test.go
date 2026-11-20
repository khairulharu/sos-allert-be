package repository

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/sirupsen/logrus"
)

func newSessionCacheTest(t *testing.T, ttl time.Duration) (*SessionCache, *SessionRepository, *miniredis.Miniredis) {
	t.Helper()
	repo, server := newSessionTestRepo(t)
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return NewSessionCache(repo, logger, ttl), repo, server
}

// countRedisGets is not available from miniredis, so the tests prove cache
// behaviour by deleting the Redis record: a cached answer must survive the
// deletion until the entry expires or is invalidated.
func TestSessionCacheServesRepeatedGetsFromMemory(t *testing.T) {
	cache, _, server := newSessionCacheTest(t, 30*time.Second)
	ctx := context.Background()

	if err := cache.Create(ctx, "user-1", "token-1", time.Hour); err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if userID, err := cache.Get(ctx, "token-1"); err != nil || userID != "user-1" {
		t.Fatalf("first Get = %q, %v; want user-1", userID, err)
	}

	// Drops the authoritative record behind the cache's back.
	server.Del(authTokenKeyPrefix + "token-1")

	if userID, err := cache.Get(ctx, "token-1"); err != nil || userID != "user-1" {
		t.Fatalf("second Get = %q, %v; want the cached user-1", userID, err)
	}
}

func TestSessionCacheExpiresAfterTTL(t *testing.T) {
	cache, _, server := newSessionCacheTest(t, 20*time.Millisecond)
	ctx := context.Background()

	if err := cache.Create(ctx, "user-1", "token-1", time.Hour); err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if _, err := cache.Get(ctx, "token-1"); err != nil {
		t.Fatalf("Get error = %v", err)
	}
	server.Del(authTokenKeyPrefix + "token-1")

	time.Sleep(40 * time.Millisecond)

	if userID, err := cache.Get(ctx, "token-1"); err != nil || userID != "" {
		t.Fatalf("Get after TTL = %q, %v; want empty (redis miss)", userID, err)
	}
}

func TestSessionCacheLogoutIsImmediate(t *testing.T) {
	cache, _, _ := newSessionCacheTest(t, 30*time.Second)
	ctx := context.Background()

	if err := cache.Create(ctx, "user-1", "token-1", time.Hour); err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if _, err := cache.Get(ctx, "token-1"); err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if err := cache.DeleteByUser(ctx, "user-1"); err != nil {
		t.Fatalf("DeleteByUser error = %v", err)
	}

	if userID, err := cache.Get(ctx, "token-1"); err != nil || userID != "" {
		t.Fatalf("Get after logout = %q, %v; want 401-worthy empty", userID, err)
	}
}

func TestSessionCacheReplacementDropsOldToken(t *testing.T) {
	cache, _, _ := newSessionCacheTest(t, 30*time.Second)
	ctx := context.Background()

	if err := cache.Create(ctx, "user-1", "token-1", time.Hour); err != nil {
		t.Fatalf("first Create error = %v", err)
	}
	if _, err := cache.Get(ctx, "token-1"); err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if err := cache.Create(ctx, "user-1", "token-2", time.Hour); err != nil {
		t.Fatalf("second Create error = %v", err)
	}

	if userID, err := cache.Get(ctx, "token-1"); err != nil || userID != "" {
		t.Fatalf("old token after re-login = %q, %v; want empty", userID, err)
	}
	if userID, err := cache.Get(ctx, "token-2"); err != nil || userID != "user-1" {
		t.Fatalf("new token = %q, %v; want user-1", userID, err)
	}
}

// A logout handled by another instance must drop this instance's cached
// entry instead of waiting for the TTL.
func TestSessionCacheInvalidationFromAnotherInstance(t *testing.T) {
	cacheA, repo, _ := newSessionCacheTest(t, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cacheA.RunInvalidation(ctx)

	// Give the subscription a moment to attach to the channel.
	waitFor(t, time.Second, func() bool {
		return repo.Client.PubSubNumSub(ctx, SessionInvalidationChannel).Val()[SessionInvalidationChannel] > 0
	})

	if err := cacheA.Create(ctx, "user-1", "token-1", time.Hour); err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if _, err := cacheA.Get(ctx, "token-1"); err != nil {
		t.Fatalf("Get error = %v", err)
	}

	// Same Redis, different process-local cache: instance B logs the user
	// out.
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	cacheB := NewSessionCache(repo, logger, 30*time.Second)
	if err := cacheB.DeleteByUser(ctx, "user-1"); err != nil {
		t.Fatalf("instance B DeleteByUser error = %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		userID, err := cacheA.Get(ctx, "token-1")
		return err == nil && userID == ""
	})
}

func TestSessionCacheRedisErrorIsNotCached(t *testing.T) {
	_, repo, _ := newSessionCacheTest(t, 30*time.Second)
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	cache := NewSessionCache(repo, logger, 30*time.Second)
	ctx := context.Background()

	if err := cache.Repo.Client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	if _, err := cache.Get(ctx, "token-1"); err == nil {
		t.Fatal("Get with a closed client = nil error, want a cache/storage error (503 path)")
	}
}

func TestSessionCacheDisabledReadsRedisEveryTime(t *testing.T) {
	cache, _, server := newSessionCacheTest(t, 0)
	ctx := context.Background()

	if err := cache.Create(ctx, "user-1", "token-1", time.Hour); err != nil {
		t.Fatalf("Create error = %v", err)
	}
	server.Del(authTokenKeyPrefix + "token-1")

	if userID, err := cache.Get(ctx, "token-1"); err != nil || userID != "" {
		t.Fatalf("Get with cache disabled = %q, %v; want empty", userID, err)
	}
}

func TestSessionCacheBounded(t *testing.T) {
	cache, _, _ := newSessionCacheTest(t, 30*time.Second)
	cache.MaxEntries = 2
	ctx := context.Background()

	for _, token := range []string{"token-1", "token-2", "token-3"} {
		if err := cache.Create(ctx, "user-"+token, token, time.Hour); err != nil {
			t.Fatalf("Create %s error = %v", token, err)
		}
		if _, err := cache.Get(ctx, token); err != nil {
			t.Fatalf("Get %s error = %v", token, err)
		}
	}

	cache.mu.Lock()
	cached := len(cache.byToken)
	cache.mu.Unlock()
	if cached > cache.MaxEntries {
		t.Fatalf("cached entries = %d, want <= %d", cached, cache.MaxEntries)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// The cache must satisfy the same SessionStore contract the use case
// expects (Create/Get/DeleteByUser).
var _ interface {
	Create(ctx context.Context, userID, token string, ttl time.Duration) error
	Get(ctx context.Context, token string) (string, error)
	DeleteByUser(ctx context.Context, userID string) error
} = (*SessionCache)(nil)
