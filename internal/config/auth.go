package config

import (
	"time"

	"github.com/spf13/viper"
)

// DefaultTokenTTL is the session lifetime used when auth.token_ttl_hours is
// unset or invalid. Sessions live in Redis and always expire: the raw token
// is no longer stored in MySQL.
const DefaultTokenTTL = 168 * time.Hour

// AuthConfig tunes the per-IP limiter on register/login and the Redis
// session lifetime.
type AuthConfig struct {
	RateLimitPerMinute int
	Window             time.Duration
	Lockout            time.Duration
	TokenTTL           time.Duration
}

// NewAuthConfig reads the optional `auth` section:
//
//	"auth": { "rate_limit_per_minute": 5, "lockout_minutes": 15, "token_ttl_hours": 168 }
//
// token_ttl_hours must stay positive so sessions cannot live forever; 0 or a
// negative value falls back to DefaultTokenTTL.
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
	return &AuthConfig{
		RateLimitPerMinute: limit,
		Window:             time.Minute,
		Lockout:            time.Duration(lockoutMinutes) * time.Minute,
		TokenTTL:           ttl,
	}
}
