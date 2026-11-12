package config

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestNewAIConfigJevDefaults(t *testing.T) {
	v := viper.New()
	v.Set("ai.enabled", true)
	v.Set("ai.provider", "jev")
	v.Set("ai.timeout_seconds", 10)
	v.Set("ai.jev.api_key", "jev_key")

	cfg := NewAIConfig(v)
	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if cfg.Provider != AITriageProviderJev {
		t.Fatalf("Provider = %q, want jev", cfg.Provider)
	}
	if cfg.JevBaseURL != "https://api.typesafe.ai/v1" {
		t.Fatalf("JevBaseURL = %q, want the TypeSafe default", cfg.JevBaseURL)
	}
	if cfg.JevModel != "jev-latest" {
		t.Fatalf("JevModel = %q, want jev-latest", cfg.JevModel)
	}
	if cfg.JevAPIKey != "jev_key" {
		t.Fatalf("JevAPIKey = %q, want jev_key", cfg.JevAPIKey)
	}
	if cfg.Timeout != 10*time.Second {
		t.Fatalf("Timeout = %v, want 10s", cfg.Timeout)
	}
}

func TestNewAIConfigJevExplicitValuesWin(t *testing.T) {
	v := viper.New()
	v.Set("ai.provider", "JEV") // provider names are case-insensitive
	v.Set("ai.jev.base_url", "http://typesafe.local/v1")
	v.Set("ai.jev.model", "jev-1.14.0")

	cfg := NewAIConfig(v)
	if cfg.Provider != AITriageProviderJev {
		t.Fatalf("Provider = %q, want jev", cfg.Provider)
	}
	if cfg.JevBaseURL != "http://typesafe.local/v1" || cfg.JevModel != "jev-1.14.0" {
		t.Fatalf("base/model = %q/%q, want the explicit values", cfg.JevBaseURL, cfg.JevModel)
	}
}

func TestNewAIConfigOllamaDefaultsUnchanged(t *testing.T) {
	v := viper.New()
	v.Set("ai.provider", "ollama")

	cfg := NewAIConfig(v)
	if cfg.OllamaBaseURL != "http://localhost:11434" {
		t.Fatalf("OllamaBaseURL = %q, want localhost", cfg.OllamaBaseURL)
	}
	if cfg.OllamaModel != "llama3" {
		t.Fatalf("OllamaModel = %q, want llama3", cfg.OllamaModel)
	}
	if cfg.Timeout != 5*time.Second {
		t.Fatalf("Timeout = %v, want 5s", cfg.Timeout)
	}
}

func TestNewAIConfigNoProviderIsOffline(t *testing.T) {
	v := viper.New()

	cfg := NewAIConfig(v)
	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true when ai.enabled is unset")
	}
	if cfg.Provider != AITriageProviderOffline {
		t.Fatalf("Provider = %q, want offline", cfg.Provider)
	}
}

func TestNewAIConfigDisabled(t *testing.T) {
	v := viper.New()
	v.Set("ai.enabled", false)

	if cfg := NewAIConfig(v); cfg.Enabled {
		t.Fatal("Enabled = true, want false when ai.enabled is false")
	}
}
