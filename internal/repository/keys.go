package repository

const (
	userLocationKey = "users:locations"
	userLastSeenKey = "users:last_seen"
	// idempotencyKeyPrefix + key -> alert id, TTL 5 minutes (README section 6).
	idempotencyKeyPrefix = "idempotency:sos:"
	// rateLimitKeyPrefix + user id -> sorted set of trigger timestamps.
	rateLimitKeyPrefix = "ratelimit:sos:"
	// classifyRateLimitKeyPrefix + user id -> sorted set of paid STT/LLM calls.
	classifyRateLimitKeyPrefix = "ratelimit:classify:"
	// authRateLimitKeyPrefix + client IP -> sorted set of auth attempts.
	authRateLimitKeyPrefix = "ratelimit:auth:"
	// authLockoutKeyPrefix + client IP -> lockout marker with TTL.
	authLockoutKeyPrefix = "lockout:auth:"
	// authTokenKeyPrefix + raw session token -> user id, with the session TTL.
	authTokenKeyPrefix = "auth:token:"
	// authUserKeyPrefix + user id -> current session token, kept so a new
	// login can invalidate the previous one (single-session).
	authUserKeyPrefix = "auth:user:"
)
