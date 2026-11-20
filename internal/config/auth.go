package config

import (
	"time"

	"github.com/spf13/viper"
)

// DefaultTokenTTL is the session lifetime used when auth.token_ttl_hours is
// unset or invalid. Sessions live in Redis and always expire: the raw token
// is no longer stored in MySQL.
const DefaultTokenTTL = 168 * time.Hour

// DefaultSessionCacheTTL is how long a verified token may be reused without
// a Redis round trip. Session verification runs on every authenticated
// request, and the managed Redis sits ~170 ms away, so a short cache removes
// that constant from every route. 0 disables the cache.
const DefaultSessionCacheTTL = 30 * time.Second

// AuthConfig tunes the per-IP limiter on register/login and the Redis
// session lifetime.
type AuthConfig struct {
	RateLimitPerMinute int
	Window             time.Duration
	Lockout            time.Duration
	TokenTTL           time.Duration
	// SessionCacheTTL bounds how long an in-process copy of a verified
	// session may be served after a Redis read. Logout and account delete
	// invalidate it immediately (in-process and via pub/sub); the TTL only
	// bounds a lost invalidation.
	SessionCacheTTL time.Duration
}

// NewAuthConfig reads the optional `auth` section:
//
//	"auth": { "rate_limit_per_minute": 5, "lockout_minutes": 15, "token_ttl_hours": 168,
//	          "session_cache_ttl_seconds": 30 }
//
// token_ttl_hours must stay positive so sessions cannot live forever; 0 or a
// negative value falls back to DefaultTokenTTL. session_cache_ttl_seconds
// has no default when unset (DefaultSessionCacheTTL); an explicit 0 disables
// the cache, and the value is capped at the token TTL so a cached entry can
// never outlive the session it points at.
func NewAuthConfig(v *viper.Viper) *AuthConfig {
	limit := v.GetInt("auth.rate_limit_per_minute")
	if limit <= 0 {
		limit = 5
	}
	lockoutMinutes := v.GetInt("auth.lockout_minutes")
	if lockoutMinutes <= 0 {
		lockoutMinutes = 15
	}
	ttl := DefaultTokenTTL
	if hours := v.GetInt("auth.token_ttl_hours"); hours > 0 {
		ttl = time.Duration(hours) * time.Hour
	}

	cacheTTL := DefaultSessionCacheTTL
	if v.IsSet("auth.session_cache_ttl_seconds") {
		switch seconds := v.GetInt("auth.session_cache_ttl_seconds"); {
		case seconds < 0:
			cacheTTL = DefaultSessionCacheTTL
		case seconds == 0:
			cacheTTL = 0
		default:
			cacheTTL = time.Duration(seconds) * time.Second
		}
	}
	if cacheTTL > ttl {
		cacheTTL = ttl
	}

	return &AuthConfig{
		RateLimitPerMinute: limit,
		Window:             time.Minute,
		Lockout:            time.Duration(lockoutMinutes) * time.Minute,
		TokenTTL:           ttl,
		SessionCacheTTL:    cacheTTL,
	}
}
