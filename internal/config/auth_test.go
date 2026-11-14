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
