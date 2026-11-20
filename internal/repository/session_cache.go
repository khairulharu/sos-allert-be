package repository

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// SessionInvalidationChannel carries session changes (login replacing the
// previous token, logout, account delete) between app instances, so an
// in-process verification cache is dropped immediately everywhere instead
// of waiting for its TTL (#58).
const SessionInvalidationChannel = "auth:session_invalidate"

// defaultSessionCacheEntries bounds the local cache. It only exists to cap
// memory: an evicted entry simply costs one Redis round trip again.
const defaultSessionCacheEntries = 10_000

// sessionInvalidation is the pub/sub payload. KeepToken is set when a login
// replaced the session: the receiving instance must drop the user's old
// token but keep the new one if it already read it.
type sessionInvalidation struct {
	UserID    string `json:"user_id"`
	KeepToken string `json:"keep_token,omitempty"`
}

type sessionCacheEntry struct {
	userID    string
	expiresAt time.Time
}

// SessionCache memoises token -> userID verification in process memory so
// a polling client does not pay a Redis round trip on every authenticated
// request. Redis stays the source of truth:
//
//   - a miss reads Redis and only caches a hit;
//   - Create/DeleteByUser evict locally and publish an invalidation, so a
//     logout is visible on the next request (also on other instances);
//   - a Redis error is returned unchanged, so the caller still answers 503
//     instead of mistaking an outage for a deleted session;
//   - entries expire after TTL, which bounds staleness if a publication is
//     lost.
//
// It satisfies usecase.SessionStore.
type SessionCache struct {
	Repo *SessionRepository
	Log  *logrus.Logger
	// TTL is how long a verified token may be reused without Redis. A
	// non-positive TTL disables the cache entirely.
	TTL time.Duration
	// MaxEntries caps the map. Zero uses defaultSessionCacheEntries.
	MaxEntries int

	mu      sync.Mutex
	byToken map[string]sessionCacheEntry
	byUser  map[string]string

	// now is replaceable in tests.
	now func() time.Time
}

func NewSessionCache(repo *SessionRepository, logger *logrus.Logger, ttl time.Duration) *SessionCache {
	return &SessionCache{
		Repo:       repo,
		Log:        logger,
		TTL:        ttl,
		MaxEntries: defaultSessionCacheEntries,
		byToken:    make(map[string]sessionCacheEntry),
		byUser:     make(map[string]string),
		now:        time.Now,
	}
}

// Create stores the session in Redis and makes the new token visible
// locally before announcing it to the other instances.
func (c *SessionCache) Create(ctx context.Context, userID, token string, ttl time.Duration) error {
	if err := c.Repo.Create(ctx, userID, token, ttl); err != nil {
		return err
	}
	if !c.enabled() {
		return nil
	}
	// The login replaced this user's previous token: drop it here and
	// tell the other instances to do the same.
	c.replace(userID, token)
	c.publish(ctx, sessionInvalidation{UserID: userID, KeepToken: token})
	return nil
}

// Get resolves a raw token, preferring the local cache.
func (c *SessionCache) Get(ctx context.Context, token string) (string, error) {
	if !c.enabled() {
		return c.Repo.Get(ctx, token)
	}
	if userID, ok := c.lookup(token); ok {
		return userID, nil
	}
	userID, err := c.Repo.Get(ctx, token)
	if err != nil {
		// Never cache the error: a Redis outage must keep surfacing as a
		// retryable 503, not as an authenticated request.
		return "", err
	}
	if userID != "" {
		c.store(token, userID)
	}
	return userID, nil
}

// DeleteByUser removes the session from Redis, then from this cache, then
// from the other instances. The Redis delete runs first so a failed logout
// does not leave the caller looking signed out while the token still works.
func (c *SessionCache) DeleteByUser(ctx context.Context, userID string) error {
	if err := c.Repo.DeleteByUser(ctx, userID); err != nil {
		return err
	}
	if !c.enabled() {
		return nil
	}
	c.evict(userID, "")
	c.publish(ctx, sessionInvalidation{UserID: userID})
	return nil
}

// RunInvalidation subscribes to the invalidation channel and applies the
// session changes made by other instances until ctx is done. The
// subscription reconnects on its own (a broker blip only widens the window
// in which a stale entry survives, bounded by the TTL). Safe to call when
// the cache is disabled (it returns).
func (c *SessionCache) RunInvalidation(ctx context.Context) {
	if !c.enabled() {
		return
	}
	pubsub := c.Repo.Client.Subscribe(ctx, SessionInvalidationChannel)
	defer func() { _ = pubsub.Close() }()

	// Channel() reconnects and resubscribes after a failure; Receive()ing
	// once first would turn a transient startup error into a permanent
	// "TTL only" mode.
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-pubsub.Channel():
			if !ok {
				return
			}
			var event sessionInvalidation
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil || event.UserID == "" {
				continue
			}
			// KeepToken marks the instance that already stored the new
			// token; everywhere else the old one is dropped.
			c.evict(event.UserID, event.KeepToken)
		}
	}
}

func (c *SessionCache) enabled() bool {
	return c != nil && c.TTL > 0
}

func (c *SessionCache) maxEntries() int {
	if c.MaxEntries > 0 {
		return c.MaxEntries
	}
	return defaultSessionCacheEntries
}

func (c *SessionCache) lookup(token string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.byToken[token]
	if !ok {
		return "", false
	}
	if !c.now().Before(entry.expiresAt) {
		c.removeLocked(token, entry.userID)
		return "", false
	}
	return entry.userID, true
}

func (c *SessionCache) store(token, userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// A newer session may have been recorded while this lookup was in
	// flight; caching the old token would resurrect it.
	if current, ok := c.byUser[userID]; ok && current != token {
		return
	}
	if len(c.byToken) >= c.maxEntries() {
		c.makeRoomLocked()
	}
	c.byToken[token] = sessionCacheEntry{userID: userID, expiresAt: c.now().Add(c.TTL)}
	c.byUser[userID] = token
}

// replace drops the user's previous token (single-session) and stores the
// new one.
func (c *SessionCache) replace(userID, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if old, ok := c.byUser[userID]; ok && old != token {
		delete(c.byToken, old)
	}
	if len(c.byToken) >= c.maxEntries() {
		c.makeRoomLocked()
	}
	c.byToken[token] = sessionCacheEntry{userID: userID, expiresAt: c.now().Add(c.TTL)}
	c.byUser[userID] = token
}

// evict removes the user's cached token unless it is keepToken (the token
// the caller just stored).
func (c *SessionCache) evict(userID, keepToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	token, ok := c.byUser[userID]
	if !ok || token == keepToken {
		return
	}
	c.removeLocked(token, userID)
}

func (c *SessionCache) removeLocked(token, userID string) {
	delete(c.byToken, token)
	if current, ok := c.byUser[userID]; ok && current == token {
		delete(c.byUser, userID)
	}
}

// makeRoomLocked frees at least one slot under the entry cap: expired
// entries first, then arbitrary ones. Anything may be dropped because the
// cache only saves a round trip.
func (c *SessionCache) makeRoomLocked() {
	limit := c.maxEntries()
	now := c.now()
	for token, entry := range c.byToken {
		if len(c.byToken) < limit {
			return
		}
		if !now.Before(entry.expiresAt) {
			c.removeLocked(token, entry.userID)
		}
	}
	for token, entry := range c.byToken {
		if len(c.byToken) < limit {
			return
		}
		c.removeLocked(token, entry.userID)
	}
}

// publish announces a session change. Failure only means the other
// instances keep a stale entry until its TTL, so it must never fail the
// login/logout it belongs to. The request context is detached so a client
// disconnecting mid-response does not skip the notification.
func (c *SessionCache) publish(ctx context.Context, event sessionInvalidation) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := c.Repo.Client.Publish(ctx, SessionInvalidationChannel, payload).Err(); err != nil {
		if c.Log != nil {
			c.Log.WithError(err).Warn("could not publish session invalidation; relying on TTL")
		}
	}
}
