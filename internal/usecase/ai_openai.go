package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAIClient classifies via the OpenAI-compatible chat completions API
// (works with OpenAI and any compatible gateway). Uses response_format
// json_object and temperature 0 for a parseable, stable classification.
type OpenAIClient struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// NewOpenAIClient builds the client; a non-positive timeout defaults to
// 5s (configurable via ai.timeout_seconds).
func NewOpenAIClient(baseURL, apiKey, model string, timeout time.Duration) *OpenAIClient {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &OpenAIClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponseFormat struct {
	Type string `json:"type"` // "json_object"
}

type openAIChatRequest struct {
	Model          string               `json:"model"`
	Messages       []openAIMessage      `json:"messages"`
	Temperature    float64              `json:"temperature"`
	ResponseFormat openAIResponseFormat `json:"response_format"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
}

func (c *OpenAIClient) Triage(ctx context.Context, in TriageInput) (TriageResult, error) {
	reqBody := openAIChatRequest{
		Model: c.Model,
		Messages: []openAIMessage{
			{Role: "system", Content: triageSystemPrompt},
			{Role: "user", Content: in.Description},
		},
		Temperature:    0,
		ResponseFormat: openAIResponseFormat{Type: "json_object"},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return TriageResult{}, fmt.Errorf("ai triage openai: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return TriageResult{}, fmt.Errorf("ai triage openai: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return TriageResult{}, fmt.Errorf("ai triage openai: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return TriageResult{}, fmt.Errorf("ai triage openai: unexpected status %d", resp.StatusCode)
	}
	var chat openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chat); err != nil {
		return TriageResult{}, fmt.Errorf("ai triage openai: decode response: %w", err)
	}
	if len(chat.Choices) == 0 || strings.TrimSpace(chat.Choices[0].Message.Content) == "" {
		return TriageResult{}, fmt.Errorf("ai triage openai: empty choices")
	}
	return parseTriageJSON([]byte(chat.Choices[0].Message.Content))
}

// parseTriageJSON validates a provider's JSON payload. Invalid or
// out-of-range values are errors so the composed engine falls back to
// the deterministic offline classifier.
func parseTriageJSON(data []byte) (TriageResult, error) {
	var payload struct {
		EmergencyType string  `json:"emergency_type"`
		Severity      float64 `json:"severity"`
		Summary       string  `json:"summary"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return TriageResult{}, fmt.Errorf("ai triage: invalid provider JSON: %w", err)
	}
	switch payload.EmergencyType {
	case "DANGER", "MEDICAL", "ACCIDENT":
	default:
		return TriageResult{}, fmt.Errorf("ai triage: unknown emergency_type %q", payload.EmergencyType)
	}
	if payload.Severity < 0 || payload.Severity > 1 {
		return TriageResult{}, fmt.Errorf("ai triage: severity %v out of range", payload.Severity)
	}
	return TriageResult{
		EmergencyType: payload.EmergencyType,
		Severity:      payload.Severity,
		Summary:       strings.TrimSpace(payload.Summary),
	}, nil
}
