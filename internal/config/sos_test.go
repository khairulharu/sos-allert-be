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

// The durable grace-period outbox scan interval (#40) defaults to 1 s and
// is clamped so a misconfiguration cannot poll MySQL in a tight loop.
func TestNewSOSConfigDispatchPoll(t *testing.T) {
	cfg := NewSOSConfig(viper.New())
	if cfg.DispatchPoll != time.Second {
		t.Fatalf("DispatchPoll = %v, want the 1s default", cfg.DispatchPoll)
	}

	v := viper.New()
	v.Set("sos.dispatch_poll_seconds", 2)
	if got := NewSOSConfig(v).DispatchPoll; got != 2*time.Second {
		t.Fatalf("DispatchPoll = %v, want 2s", got)
	}

	v = viper.New()
	v.Set("sos.dispatch_poll_seconds", 0.01)
	if got := NewSOSConfig(v).DispatchPoll; got != 100*time.Millisecond {
		t.Fatalf("DispatchPoll = %v, want the 100ms floor", got)
	}
}

// Housekeeping (#42) defaults to once a minute and is clamped to one second
// so a misconfiguration cannot expire/purge in a tight loop.
func TestNewSOSConfigHousekeepingInterval(t *testing.T) {
	if got := NewSOSConfig(viper.New()).HousekeepingInterval; got != time.Minute {
		t.Fatalf("HousekeepingInterval = %v, want the 60s default", got)
	}

	v := viper.New()
	v.Set("sos.housekeeping_interval_seconds", 300)
	if got := NewSOSConfig(v).HousekeepingInterval; got != 5*time.Minute {
		t.Fatalf("HousekeepingInterval = %v, want 5m", got)
	}

	v = viper.New()
	v.Set("sos.housekeeping_interval_seconds", 0.01)
	if got := NewSOSConfig(v).HousekeepingInterval; got != time.Second {
		t.Fatalf("HousekeepingInterval = %v, want the 1s floor", got)
	}
}

// The WebSocket concurrency budgets (#42) are passed through as configured;
// zero keeps the delivery/http defaults, which the limiter resolves.
func TestNewSOSConfigWebSocketLimits(t *testing.T) {
	cfg := NewSOSConfig(viper.New())
	if cfg.WSMaxConnections != 0 || cfg.WSMaxPerIP != 0 || cfg.WSMaxPerUser != 0 {
		t.Fatalf("defaults = %d/%d/%d, want 0 (limiter defaults)",
			cfg.WSMaxConnections, cfg.WSMaxPerIP, cfg.WSMaxPerUser)
	}

	v := viper.New()
	v.Set("sos.ws_max_connections", 50)
	v.Set("sos.ws_max_per_ip", 5)
	v.Set("sos.ws_max_per_user", 2)
	cfg = NewSOSConfig(v)
	if cfg.WSMaxConnections != 50 || cfg.WSMaxPerIP != 5 || cfg.WSMaxPerUser != 2 {
		t.Fatalf("configured = %d/%d/%d, want 50/5/2",
			cfg.WSMaxConnections, cfg.WSMaxPerIP, cfg.WSMaxPerUser)
	}
}
