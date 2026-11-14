package config

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestNewSOSConfigRateLimitDefaultsOn(t *testing.T) {
	cfg := NewSOSConfig(viper.New())
	if cfg.RateLimitDisabled {
		t.Fatal("RateLimitDisabled = true, want false when sos.rate_limit_disabled is absent")
	}
	if cfg.RateLimitPerHour != 3 || cfg.RateLimitWindow != time.Hour {
		t.Fatalf("limit/window = %d/%v, want 3/1h", cfg.RateLimitPerHour, cfg.RateLimitWindow)
	}
}

func TestNewSOSConfigRateLimitDisabled(t *testing.T) {
	v := viper.New()
	v.Set("sos.rate_limit_disabled", true)

	cfg := NewSOSConfig(v)
	if !cfg.RateLimitDisabled {
		t.Fatal("RateLimitDisabled = false, want true")
	}
	// The threshold still resolves to the README default so re-enabling
	// the switch restores the documented behaviour.
	if cfg.RateLimitPerHour != 3 {
		t.Fatalf("RateLimitPerHour = %d, want 3", cfg.RateLimitPerHour)
	}
}

func TestNewSOSConfigClassifyLimitDefaultsOn(t *testing.T) {
	cfg := NewSOSConfig(viper.New())
	if cfg.ClassifyRateLimitPerHour != 10 {
		t.Fatalf("ClassifyRateLimitPerHour = %d, want 10", cfg.ClassifyRateLimitPerHour)
	}

	v := viper.New()
	v.Set("sos.classify_rate_limit_per_hour", 4)
	if got := NewSOSConfig(v).ClassifyRateLimitPerHour; got != 4 {
		t.Fatalf("ClassifyRateLimitPerHour = %d, want 4", got)
	}
}
