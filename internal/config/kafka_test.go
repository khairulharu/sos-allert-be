package config

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func TestNewKafkaConsumerConfigDefaults(t *testing.T) {
	cfg := NewKafkaConsumerConfig(viper.New())

	if cfg.MaxRetries != DefaultKafkaConsumerMaxRetries {
		t.Fatalf("MaxRetries = %d, want %d", cfg.MaxRetries, DefaultKafkaConsumerMaxRetries)
	}
	if cfg.RetryBackoff != DefaultKafkaConsumerRetryBackoff {
		t.Fatalf("RetryBackoff = %v, want %v", cfg.RetryBackoff, DefaultKafkaConsumerRetryBackoff)
	}
	if cfg.DLQTopic != DefaultKafkaDLQTopic {
		t.Fatalf("DLQTopic = %q, want %q", cfg.DLQTopic, DefaultKafkaDLQTopic)
	}
}

func TestNewKafkaConsumerConfigReadsKeys(t *testing.T) {
	v := viper.New()
	v.SetConfigType("json")
	raw := `{
	  "kafka": {
	    "consumer": {"max_retries": 5, "retry_backoff_seconds": 7},
	    "dlq": {"topic": " sos.custom.dlq "}
	  }
	}`
	if err := v.ReadConfig(strings.NewReader(raw)); err != nil {
		t.Fatalf("read config: %v", err)
	}

	cfg := NewKafkaConsumerConfig(v)
	if cfg.MaxRetries != 5 {
		t.Fatalf("MaxRetries = %d, want 5", cfg.MaxRetries)
	}
	if cfg.RetryBackoff != 7*time.Second {
		t.Fatalf("RetryBackoff = %v, want 7s", cfg.RetryBackoff)
	}
	if cfg.DLQTopic != "sos.custom.dlq" {
		t.Fatalf("DLQTopic = %q, want the trimmed topic", cfg.DLQTopic)
	}
}

// An explicitly empty topic disables dead-lettering while a negative
// retry count means "no retries"; both are opt-in operator choices.
func TestNewKafkaConsumerConfigHonoursOptOut(t *testing.T) {
	v := viper.New()
	v.SetConfigType("json")
	raw := `{
	  "kafka": {
	    "consumer": {"max_retries": -1},
	    "dlq": {"topic": ""}
	  }
	}`
	if err := v.ReadConfig(strings.NewReader(raw)); err != nil {
		t.Fatalf("read config: %v", err)
	}

	cfg := NewKafkaConsumerConfig(v)
	if cfg.MaxRetries != -1 {
		t.Fatalf("MaxRetries = %d, want -1", cfg.MaxRetries)
	}
	if cfg.DLQTopic != "" {
		t.Fatalf("DLQTopic = %q, want empty (dead-lettering disabled)", cfg.DLQTopic)
	}
	if cfg.RetryBackoff != DefaultKafkaConsumerRetryBackoff {
		t.Fatalf("RetryBackoff = %v, want the default", cfg.RetryBackoff)
	}
}

// Each consumed topic gets its own consumer group so a rebalance on one
// topic cannot disturb the others (#42).
func TestKafkaGroupIDPerTopic(t *testing.T) {
	cases := map[string]string{
		"users":          "sos-allert-users",
		"contacts":       "sos-allert-contacts",
		"addresses":      "sos-allert-addresses",
		"sos.dispatched": "sos-allert",
	}
	seen := map[string]string{}
	for topic, want := range cases {
		got := KafkaGroupID("sos-allert", topic)
		if got != want {
			t.Fatalf("KafkaGroupID(%q) = %q, want %q", topic, got, want)
		}
		if other, dup := seen[got]; dup {
			t.Fatalf("topics %q and %q share the group %q", other, topic, got)
		}
		seen[got] = topic
	}

	// The base is trimmed, and an empty base falls back to the default so
	// sarama never receives an empty group id.
	if got := KafkaGroupID("  custom ", "users"); got != "custom-users" {
		t.Fatalf("KafkaGroupID with padded base = %q, want custom-users", got)
	}
	if got := KafkaGroupID("", "users"); got != DefaultKafkaGroupID+"-users" {
		t.Fatalf("KafkaGroupID with empty base = %q, want the default base", got)
	}

	// An unknown topic still gets a unique, DNS-safe suffix.
	if got := KafkaGroupID("base", "some.topic"); got != "base-some-topic" {
		t.Fatalf("KafkaGroupID(unknown topic) = %q, want base-some-topic", got)
	}
	if got := KafkaGroupID("base", ""); got != "base-default" {
		t.Fatalf("KafkaGroupID(empty topic) = %q, want base-default", got)
	}
}

// The connect retry is bounded: it stops after the configured attempts and
// returns the last error instead of looping forever.
func TestConnectWithRetry(t *testing.T) {
	t.Run("succeeds on a later attempt", func(t *testing.T) {
		calls := 0
		var slept []time.Duration
		err := connectWithRetry(KafkaConnectPolicy{Attempts: 3, Backoff: time.Second},
			func(d time.Duration) { slept = append(slept, d) },
			func() error {
				calls++
				if calls < 3 {
					return errors.New("broker unavailable")
				}
				return nil
			})
		if err != nil {
			t.Fatalf("connectWithRetry error = %v, want nil", err)
		}
		if calls != 3 {
			t.Fatalf("attempts = %d, want 3", calls)
		}
		if len(slept) != 2 || slept[0] != time.Second || slept[1] != 2*time.Second {
			t.Fatalf("backoffs = %v, want 1s then 2s", slept)
		}
	})

	t.Run("gives up after the attempts", func(t *testing.T) {
		calls := 0
		boom := errors.New("broker unavailable")
		err := connectWithRetry(KafkaConnectPolicy{Attempts: 2, Backoff: 10 * time.Millisecond},
			func(time.Duration) {},
			func() error { calls++; return boom })
		if !errors.Is(err, boom) {
			t.Fatalf("connectWithRetry error = %v, want %v", err, boom)
		}
		if calls != 2 {
			t.Fatalf("attempts = %d, want 2 (bounded, not infinite)", calls)
		}
	})

	t.Run("zero backoff never sleeps", func(t *testing.T) {
		slept := 0
		_ = connectWithRetry(KafkaConnectPolicy{Attempts: 3},
			func(time.Duration) { slept++ },
			func() error { return errors.New("down") })
		if slept != 0 {
			t.Fatalf("sleeps = %d, want 0 with a zero backoff", slept)
		}
	})
}

func TestNewKafkaConnectPolicy(t *testing.T) {
	if got := NewKafkaConnectPolicy(viper.New()); got.Attempts != DefaultKafkaConnectAttempts || got.Backoff != DefaultKafkaConnectBackoff {
		t.Fatalf("defaults = %+v, want %d attempts / %v", got, DefaultKafkaConnectAttempts, DefaultKafkaConnectBackoff)
	}

	v := viper.New()
	v.Set("kafka.connect.max_attempts", 0)
	v.Set("kafka.connect.retry_backoff_seconds", -3)
	got := NewKafkaConnectPolicy(v)
	if got.Attempts != 1 {
		t.Fatalf("Attempts = %d, want the 1 floor", got.Attempts)
	}
	if got.Backoff != 0 {
		t.Fatalf("Backoff = %v, want 0 (negative clamped)", got.Backoff)
	}
}

// Kafka is optional: an unreachable broker must yield nil (the service keeps
// running) instead of a fatal exit (#42).
func TestKafkaClientsReturnNilWhenBrokerIsUnreachable(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)

	newConfig := func() *viper.Viper {
		v := viper.New()
		v.Set("kafka.bootstrap.servers", "127.0.0.1:1")
		v.Set("kafka.group.id", "cycle-test")
		v.Set("kafka.connect.max_attempts", 1)
		v.Set("kafka.connect.retry_backoff_seconds", 0)
		return v
	}

	if group := NewKafkaConsumerGroup(newConfig(), log, "users"); group != nil {
		_ = group.Close()
		t.Fatal("NewKafkaConsumerGroup = non-nil, want nil when the broker is unreachable")
	}

	producerConfig := newConfig()
	producerConfig.Set("kafka.producer.enabled", true)
	if producer := NewKafkaProducer(producerConfig, log); producer != nil {
		_ = producer.Close()
		t.Fatal("NewKafkaProducer = non-nil, want nil when the broker is unreachable")
	}

	// Disabled producer stays nil without touching the network.
	disabled := viper.New()
	if producer := NewKafkaProducer(disabled, log); producer != nil {
		t.Fatal("NewKafkaProducer with kafka.producer.enabled=false = non-nil")
	}
}
