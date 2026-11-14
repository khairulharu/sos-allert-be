package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// IdempotencyRepository stores X-Idempotency-Key -> alert id in Redis
// with a 5-minute TTL, so mobile retries on flaky networks cannot create
// duplicate alerts.
type IdempotencyRepository struct {
	Client *redis.Client
}

func NewIdempotencyRepository(client *redis.Client) *IdempotencyRepository {
	return &IdempotencyRepository{Client: client}
}

const idempotencyTTL = 5 * time.Minute
const idempotencyPending = "pending"

// Claim reserves a key. It returns true when this caller owns the key.
func (r *IdempotencyRepository) Claim(ctx context.Context, key string) (bool, error) {
	ok, err := r.Client.SetNX(ctx, idempotencyKeyPrefix+key, idempotencyPending, idempotencyTTL).Result()
	if err != nil {
		return false, fmt.Errorf("redis idempotency claim: %w", err)
	}
	return ok, nil
}

// Get returns the stored alert id, or "" when the key is pending/absent.
func (r *IdempotencyRepository) Get(ctx context.Context, key string) (string, error) {
	val, err := r.Client.Get(ctx, idempotencyKeyPrefix+key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redis idempotency get: %w", err)
	}
	if val == idempotencyPending {
		return "", nil
	}
	return val, nil
}

// Set binds a finished alert id to the key.
func (r *IdempotencyRepository) Set(ctx context.Context, key, alertID string) error {
	if err := r.Client.Set(ctx, idempotencyKeyPrefix+key, alertID, idempotencyTTL).Err(); err != nil {
		return fmt.Errorf("redis idempotency set: %w", err)
	}
	return nil
}

// RateLimitRepository implements a Redis sliding-window limiter:
// max `limit` SOS triggers per `window` per user.
type RateLimitRepository struct {
	Client *redis.Client
}

func NewRateLimitRepository(client *redis.Client) *RateLimitRepository {
	return &RateLimitRepository{Client: client}
}

var rateLimitScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local windowMs = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]
redis.call("ZREMRANGEBYSCORE", key, 0, now - windowMs)
local count = redis.call("ZCARD", key)
if count >= limit then
  return 0
end
redis.call("ZADD", key, now, member)
redis.call("PEXPIRE", key, windowMs)
return 1
`)

// Allow returns true when the trigger is within the limit.
func (r *RateLimitRepository) Allow(ctx context.Context, userID string, limit int, window time.Duration) (bool, error) {
	return r.allow(ctx, rateLimitKeyPrefix+userID, limit, window)
}

// AllowClassify limits the paid pre-trigger classification calls per user.
func (r *RateLimitRepository) AllowClassify(ctx context.Context, userID string, limit int, window time.Duration) (bool, error) {
	return r.allow(ctx, classifyRateLimitKeyPrefix+userID, limit, window)
}

func (r *RateLimitRepository) allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	now := time.Now().UnixMilli()
	// Unique member: calls inside the same millisecond must still each
	// count, otherwise a parallel burst slips past the limit.
	member := fmt.Sprintf("%d-%d", now, time.Now().UnixNano())
	res, err := rateLimitScript.Run(ctx, r.Client, []string{key},
		now, window.Milliseconds(), limit, member).Int()
	if err != nil {
		return false, fmt.Errorf("redis rate limit: %w", err)
	}
	return res == 1, nil
}

// authRateLimitScript is a per-IP sliding window plus a lockout key. It
// allows `limit` attempts per `window`; the next attempt sets a lockout
// marker for `lockoutMs`. While the lockout key exists, every attempt is
// rejected and the remaining TTL is returned for Retry-After.
var authRateLimitScript = redis.NewScript(`
local windowKey = KEYS[1]
local lockKey = KEYS[2]
local now = tonumber(ARGV[1])
local windowMs = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local lockoutMs = tonumber(ARGV[4])
local member = ARGV[5]

local locked = redis.call("PTTL", lockKey)
if locked > 0 then
  return {0, locked}
end

redis.call("ZREMRANGEBYSCORE", windowKey, 0, now - windowMs)
local count = redis.call("ZCARD", windowKey)
if count >= limit then
  redis.call("SET", lockKey, "1", "PX", lockoutMs)
  return {0, lockoutMs}
end

redis.call("ZADD", windowKey, now, member)
redis.call("PEXPIRE", windowKey, windowMs)
return {1, 0}
`)

// AllowAuth applies the per-IP auth limiter. It returns whether the
// attempt is allowed and, when blocked, how long until the lockout ends.
func (r *RateLimitRepository) AllowAuth(ctx context.Context, ip string, limit int, window, lockout time.Duration) (bool, time.Duration, error) {
	now := time.Now().UnixMilli()
	res, err := authRateLimitScript.Run(ctx, r.Client,
		[]string{authRateLimitKeyPrefix + ip, authLockoutKeyPrefix + ip},
		now, window.Milliseconds(), limit, lockout.Milliseconds(),
		fmt.Sprintf("%d-%d", now, time.Now().UnixNano()),
	).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("redis auth rate limit: %w", err)
	}
	if len(res) != 2 {
		return false, 0, fmt.Errorf("redis auth rate limit: unexpected reply %v", res)
	}
	return res[0] == 1, time.Duration(res[1]) * time.Millisecond, nil
}
