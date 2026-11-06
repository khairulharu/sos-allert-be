package config

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestNewSTTConfigDefaultsToGroq(t *testing.T) {
	v := viper.New()
	v.Set("ai.stt.enabled", true)
	v.Set("ai.stt.api_key", "gsk_test")

	cfg := NewSTTConfig(v)
	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if cfg.Provider != "groq" {
		t.Fatalf("Provider = %q, want groq", cfg.Provider)
	}
	if cfg.BaseURL != "https://api.groq.com/openai/v1" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.Model != "whisper-large-v3-turbo" || cfg.Language != "id" {
		t.Fatalf("model/language = %q/%q", cfg.Model, cfg.Language)
	}
	if cfg.Timeout != 30*time.Second {
		t.Fatalf("Timeout = %v, want 30s", cfg.Timeout)
	}
}

func TestNewSTTConfigOpenAIFallsBackToOpenAIKey(t *testing.T) {
	v := viper.New()
	v.Set("ai.stt.enabled", true)
	v.Set("ai.stt.provider", "openai")
	v.Set("ai.openai.api_key", "sk_test")

	cfg := NewSTTConfig(v)
	if cfg.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.APIKey != "sk_test" {
		t.Fatalf("APIKey = %q", cfg.APIKey)
	}
	if cfg.Model != "whisper-1" {
		t.Fatalf("Model = %q", cfg.Model)
	}
}

func TestNewSTTConfigLocalAllowsEmptyKey(t *testing.T) {
	v := viper.New()
	v.Set("ai.stt.enabled", true)
	v.Set("ai.stt.provider", "local")
	v.Set("ai.stt.base_url", "http://localhost:8000/v1")

	cfg := NewSTTConfig(v)
	if cfg.Provider != "local" || cfg.BaseURL != "http://localhost:8000/v1" || cfg.APIKey != "" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestNewSTTConfigDisabledByDefault(t *testing.T) {
	cfg := NewSTTConfig(viper.New())
	if cfg.Enabled {
		t.Fatal("Enabled = true, want false")
	}
}
