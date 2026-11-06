package config

import (
	"time"

	"github.com/spf13/viper"
)

// AuthConfig tunes the per-IP limiter on register/login.
type AuthConfig struct {
	RateLimitPerMinute int
	Window             time.Duration
	Lockout            time.Duration
}

// NewAuthConfig reads the optional `auth` section:
//
//	"auth": { "rate_limit_per_minute": 5, "lockout_minutes": 15 }
func NewAuthConfig(v *viper.Viper) *AuthConfig {
	limit := v.GetInt("auth.rate_limit_per_minute")
	if limit <= 0 {
		limit = 5
	}
	lockoutMinutes := v.GetInt("auth.lockout_minutes")
	if lockoutMinutes <= 0 {
		lockoutMinutes = 15
	}
	return &AuthConfig{
		RateLimitPerMinute: limit,
		Window:             time.Minute,
		Lockout:            time.Duration(lockoutMinutes) * time.Minute,
	}
}
