package config

import (
	"strings"
	"time"

	"github.com/spf13/viper"
)

// AI triage provider names for the `ai.provider` key.
const (
	AITriageProviderOffline = "offline"
	AITriageProviderOllama  = "ollama"
	AITriageProviderOpenAI  = "openai"
	AITriageProviderJev     = "jev"
)

// AIConfig carries the optional AI triage settings. Nothing here is
// fatal: when AI is disabled, the SOS flow behaves exactly as before;
// when the live provider (jev/ollama/openai) is unset or fails, the
// offline keyword classifier is used.
//
// Each live provider has its own config section: ai.jev.* for TypeSafe
// Jev, ai.ollama.* for a local Ollama server, and ai.openai.* for any
// OpenAI-compatible chat completions endpoint.
type AIConfig struct {
	Enabled       bool
	Provider      string
	JevBaseURL    string
	JevModel      string
	JevAPIKey     string
	OllamaBaseURL string
	OllamaModel   string
	OpenAIBaseURL string
	OpenAIAPIKey  string
	OpenAIModel   string
	Timeout       time.Duration
}

// NewAIConfig reads the `ai` section. Defaults: enabled (bounded to the
// offline rule-based classifier, which needs no credentials), provider
// "offline", Jev at api.typesafe.ai/v1/jev-latest, Ollama at
// localhost:11434/llama3, OpenAI at api.openai.com/v1/gpt-4o-mini.
// Because viper returns false for a missing key, enabled defaults to true
// only when `ai.enabled` is unset.
func NewAIConfig(v *viper.Viper) *AIConfig {
	cfg := &AIConfig{
		Provider:      strings.ToLower(strings.TrimSpace(v.GetString("ai.provider"))),
		JevBaseURL:    strings.TrimSpace(v.GetString("ai.jev.base_url")),
		JevModel:      strings.TrimSpace(v.GetString("ai.jev.model")),
		JevAPIKey:     strings.TrimSpace(v.GetString("ai.jev.api_key")),
		OllamaBaseURL: strings.TrimSpace(v.GetString("ai.ollama.base_url")),
		OllamaModel:   strings.TrimSpace(v.GetString("ai.ollama.model")),
		OpenAIBaseURL: v.GetString("ai.openai.base_url"),
		OpenAIAPIKey:  v.GetString("ai.openai.api_key"),
		OpenAIModel:   v.GetString("ai.openai.model"),
		Timeout:       time.Duration(v.GetInt("ai.timeout_seconds")) * time.Second,
	}
	if !v.IsSet("ai.enabled") {
		cfg.Enabled = true
	} else {
		cfg.Enabled = v.GetBool("ai.enabled")
	}
	if cfg.Provider == "" {
		cfg.Provider = AITriageProviderOffline
	}
	if cfg.JevBaseURL == "" {
		cfg.JevBaseURL = "https://api.typesafe.ai/v1"
	}
	if cfg.JevModel == "" {
		cfg.JevModel = "jev-latest"
	}
	if cfg.OllamaBaseURL == "" {
		cfg.OllamaBaseURL = "http://localhost:11434"
	}
	if cfg.OllamaModel == "" {
		cfg.OllamaModel = "llama3"
	}
	if cfg.OpenAIBaseURL == "" {
		cfg.OpenAIBaseURL = "https://api.openai.com/v1"
	}
	if cfg.OpenAIModel == "" {
		cfg.OpenAIModel = "gpt-4o-mini"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	return cfg
}
