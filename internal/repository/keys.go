package repository

const (
	userLocationKey = "users:locations"
	userLastSeenKey = "users:last_seen"
	// idempotencyKeyPrefix + key -> alert id, TTL 5 minutes (README section 6).
	idempotencyKeyPrefix = "idempotency:sos:"
	// rateLimitKeyPrefix + user id -> sorted set of trigger timestamps.
	rateLimitKeyPrefix = "ratelimit:sos:"
)
