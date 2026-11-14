package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// SessionRepository stores authenticated sessions in Redis instead of the
// users table, so tokens are never persisted as plaintext in MySQL and can
// expire. Two keys back one session:
//
//	auth:token:<token> -> user id
//	auth:user:<user id> -> token (single-session: the latest login wins)
//
// Both keys share the same TTL, so the reverse mapping can never outlive the
// token it points at.
type SessionRepository struct {
	Client *redis.Client
}

func NewSessionRepository(client *redis.Client) *SessionRepository {
	return &SessionRepository{Client: client}
}

// sessionCreateScript atomically drops the caller's previous token and stores
// the new one. KEYS[1] is the new token key, KEYS[2] the user -> token key.
// ARGV: user id, new token, ttl seconds, token key prefix.
var sessionCreateScript = redis.NewScript(`
local old = redis.call("GET", KEYS[2])
if old and ARGV[4] and old ~= ARGV[2] then
  redis.call("DEL", ARGV[4] .. old)
end
if ARGV[3] == "0" then
  redis.call("SET", KEYS[1], ARGV[1])
  redis.call("SET", KEYS[2], ARGV[2])
else
  redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[3])
  redis.call("SET", KEYS[2], ARGV[2], "EX", ARGV[3])
end
return 1
`)

// sessionDeleteScript removes both keys for a user. KEYS[1] is the user ->
// token key; ARGV[1] is the token key prefix.
var sessionDeleteScript = redis.NewScript(`
local token = redis.call("GET", KEYS[1])
if token and ARGV[1] then
  redis.call("DEL", ARGV[1] .. token)
end
redis.call("DEL", KEYS[1])
return 1
`)

// Create stores a new session for userID and invalidates the previous one.
// A non-positive ttl is treated as "no expiry" defensively; config always
// supplies a positive TTL.
func (r *SessionRepository) Create(ctx context.Context, userID, token string, ttl time.Duration) error {
	seconds := int64(0)
	if ttl > 0 {
		seconds = int64(ttl.Seconds())
	}
	err := sessionCreateScript.Run(ctx, r.Client,
		[]string{authTokenKeyPrefix + token, authUserKeyPrefix + userID},
		userID, token, seconds, authTokenKeyPrefix,
	).Err()
	if err != nil {
		return fmt.Errorf("redis session create: %w", err)
	}
	return nil
}

// Get returns the user id bound to token, or "" when the session is absent or
// expired.
func (r *SessionRepository) Get(ctx context.Context, token string) (string, error) {
	val, err := r.Client.Get(ctx, authTokenKeyPrefix+token).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redis session get: %w", err)
	}
	return val, nil
}

// DeleteByUser removes the current session of userID.
func (r *SessionRepository) DeleteByUser(ctx context.Context, userID string) error {
	err := sessionDeleteScript.Run(ctx, r.Client,
		[]string{authUserKeyPrefix + userID}, authTokenKeyPrefix,
	).Err()
	if err != nil {
		return fmt.Errorf("redis session delete: %w", err)
	}
	return nil
}
