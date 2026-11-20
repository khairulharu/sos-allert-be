package config

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestNewAuthConfigDefaults(t *testing.T) {
	cfg := NewAuthConfig(viper.New())

	if cfg.RateLimitPerMinute != 5 {
		t.Errorf("RateLimitPerMinute = %d, want 5", cfg.RateLimitPerMinute)
	}
	if cfg.Lockout != 15*time.Minute {
		t.Errorf("Lockout = %v, want 15m", cfg.Lockout)
	}
	if cfg.TokenTTL != 168*time.Hour {
		t.Errorf("TokenTTL = %v, want 168h", cfg.TokenTTL)
	}
	if cfg.SessionCacheTTL != DefaultSessionCacheTTL {
		t.Errorf("SessionCacheTTL = %v, want %v", cfg.SessionCacheTTL, DefaultSessionCacheTTL)
	}
}

func TestNewAuthConfigSessionCacheTTL(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	tests := []struct {
		name     string
		seconds  *int
		tokenHrs int
		want     time.Duration
	}{
		{name: "explicit seconds", seconds: intPtr(60), tokenHrs: 168, want: time.Minute},
		{name: "zero disables the cache", seconds: intPtr(0), tokenHrs: 168, want: 0},
		{name: "negative falls back", seconds: intPtr(-1), tokenHrs: 168, want: DefaultSessionCacheTTL},
		{name: "capped at the token TTL", seconds: intPtr(7200), tokenHrs: 1, want: time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			v.Set("auth.token_ttl_hours", tc.tokenHrs)
			if tc.seconds != nil {
				v.Set("auth.session_cache_ttl_seconds", *tc.seconds)
			}

			if got := NewAuthConfig(v).SessionCacheTTL; got != tc.want {
				t.Errorf("SessionCacheTTL = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewAuthConfigTokenTTL(t *testing.T) {
	tests := []struct {
		name  string
		hours int
		want  time.Duration
	}{
		{name: "explicit hours", hours: 24, want: 24 * time.Hour},
		{name: "zero falls back", hours: 0, want: DefaultTokenTTL},
		{name: "negative falls back", hours: -5, want: DefaultTokenTTL},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			v.Set("auth.token_ttl_hours", tc.hours)

			if got := NewAuthConfig(v).TokenTTL; got != tc.want {
				t.Errorf("TokenTTL = %v, want %v", got, tc.want)
			}
		})
	}
}
