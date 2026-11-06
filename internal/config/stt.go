package config

import (
	"strings"
	"time"

	"github.com/spf13/viper"
)

// STTConfig carries the optional speech-to-text provider settings.
// The client is OpenAI-compatible, so Groq, OpenAI, and local
// faster-whisper/whisper.cpp servers all work with the same fields.
type STTConfig struct {
	Enabled  bool
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
	Language string
	Timeout  time.Duration
}

// NewSTTConfig reads the optional `ai.stt` section. Defaults follow the
// Groq transcription endpoint; the feature is disabled unless
// ai.stt.enabled is true.
func NewSTTConfig(v *viper.Viper) *STTConfig {
	cfg := &STTConfig{
		Enabled:  v.GetBool("ai.stt.enabled"),
		Provider: strings.ToLower(v.GetString("ai.stt.provider")),
		BaseURL:  strings.TrimRight(v.GetString("ai.stt.base_url"), "/"),
		APIKey:   v.GetString("ai.stt.api_key"),
		Model:    v.GetString("ai.stt.model"),
		Language: v.GetString("ai.stt.language"),
		Timeout:  time.Duration(v.GetInt("ai.stt.timeout_seconds")) * time.Second,
	}

	if cfg.Provider == "" {
		cfg.Provider = "groq"
	}
	switch cfg.Provider {
	case "local":
		// base_url must point at an OpenAI-compatible local server.
	case "openai":
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.openai.com/v1"
		}
		if cfg.APIKey == "" {
			cfg.APIKey = v.GetString("ai.openai.api_key")
		}
	default: // groq
		cfg.Provider = "groq"
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.groq.com/openai/v1"
		}
	}

	if cfg.Model == "" {
		if cfg.Provider == "openai" {
			cfg.Model = "whisper-1"
		} else {
			cfg.Model = "whisper-large-v3-turbo"
		}
	}
	if cfg.Language == "" {
		cfg.Language = "id"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return cfg
}
