package config

import (
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// AIConfig carries the optional hosted-model triage settings. The
// service works without them: with no provider configured, triage
// runs on the deterministic rule classifier in internal/ai, and the
// LLM path is only a quality upgrade, never a requirement for SOS.
// API keys must never be committed: put them in config.json (which
// is gitignored) or in the AI_API_KEY environment variable, which
// takes precedence for local/demo runs.
type AIConfig struct {
	Enabled        bool
	BaseURL        string
	APIKey         string
	Model          string
	Timeout        time.Duration
	Provider       string
	RuleOnlyReason string
}

// NewAIConfig reads the `ai` section. Enabled is true only when the
// section is enabled and a base URL, API key, and model are all
// present; otherwise the caller must run rule-only triage.
func NewAIConfig(v *viper.Viper, log *logrus.Logger) *AIConfig {
	cfg := &AIConfig{
		Enabled:  v.GetBool("ai.enabled"),
		BaseURL:  v.GetString("ai.base_url"),
		APIKey:   v.GetString("ai.api_key"),
		Model:    v.GetString("ai.model"),
		Provider: v.GetString("ai.provider"),
		Timeout:  time.Duration(v.GetInt("ai.timeout_seconds")) * time.Second,
	}
	if cfg.Provider == "" {
		cfg.Provider = "openai-compatible"
	}
	if env := os.Getenv("AI_API_KEY"); env != "" {
		cfg.APIKey = env
	}
	if env := os.Getenv("AI_BASE_URL"); env != "" {
		cfg.BaseURL = env
	}
	if env := os.Getenv("AI_MODEL"); env != "" {
		cfg.Model = env
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if !cfg.Enabled {
		cfg.RuleOnlyReason = "ai.enabled is false"
	} else if cfg.BaseURL == "" || cfg.APIKey == "" || cfg.Model == "" {
		cfg.Enabled = false
		cfg.RuleOnlyReason = "ai.base_url, ai.api_key, and ai.model must all be set for LLM triage"
	}
	if !cfg.Enabled && log != nil {
		log.Warnf("AI triage running in rule-only mode: %s", cfg.RuleOnlyReason)
	}
	return cfg
}
