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
redis.call("ZREMRANGEBYSCORE", key, 0, now - windowMs)
local count = redis.call("ZCARD", key)
if count >= limit then
  return 0
end
redis.call("ZADD", key, now, now)
redis.call("PEXPIRE", key, windowMs)
return 1
`)

// Allow returns true when the trigger is within the limit.
func (r *RateLimitRepository) Allow(ctx context.Context, userID string, limit int, window time.Duration) (bool, error) {
	now := time.Now().UnixMilli()
	res, err := rateLimitScript.Run(ctx, r.Client, []string{rateLimitKeyPrefix + userID},
		now, window.Milliseconds(), limit).Int()
	if err != nil {
		return false, fmt.Errorf("redis rate limit: %w", err)
	}
	return res == 1, nil
}
