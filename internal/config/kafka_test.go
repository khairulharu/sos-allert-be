package config

import (
	"strings"
	"testing"
	"time"

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
