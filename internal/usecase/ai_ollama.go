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

// triageSystemPrompt asks the LLM for strict, tiny JSON. "format": "json"
// on Ollama guarantees JSON output for most models.
const triageSystemPrompt = `You are the emergency triage classifier of an SOS safety app operating in Bali, Indonesia.
Classify a short user distress message and respond ONLY with a JSON object of the form:
{"emergency_type": "DANGER|MEDICAL|ACCIDENT", "severity": <number 0.0 to 1.0>, "summary": "<one short sentence>"}
DANGER = immediate threat or violence, MEDICAL = health emergency, ACCIDENT = crash/fall/vehicle incident.
The summary must be in Indonesian, max 15 words. Do not output anything but the JSON object.`

// OllamaClient classifies via a local Ollama server (/api/chat with
// structured JSON output). Any HTTP/parse failure surfaces as an error
// so the composed engine can fall back to the offline classifier.
type OllamaClient struct {
	BaseURL string
	Model   string
	HTTP    *http.Client
}

// NewOllamaClient builds the client; a non-positive timeout defaults to
// 5s so a hung local model cannot stall the SOS request indefinitely.
func NewOllamaClient(baseURL, model string, timeout time.Duration) *OllamaClient {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &OllamaClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Format   string          `json:"format"` // "json" requests structured output
}

type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
	Done    bool          `json:"done"`
}

func (c *OllamaClient) Triage(ctx context.Context, in TriageInput) (TriageResult, error) {
	reqBody := ollamaChatRequest{
		Model: c.Model,
		Messages: []ollamaMessage{
			{Role: "system", Content: triageSystemPrompt},
			{Role: "user", Content: in.Description},
		},
		Stream: false,
		Format: "json",
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return TriageResult{}, fmt.Errorf("ai triage ollama: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(raw))
	if err != nil {
		return TriageResult{}, fmt.Errorf("ai triage ollama: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return TriageResult{}, fmt.Errorf("ai triage ollama: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return TriageResult{}, fmt.Errorf("ai triage ollama: unexpected status %d", resp.StatusCode)
	}
	var chat ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chat); err != nil {
		return TriageResult{}, fmt.Errorf("ai triage ollama: decode response: %w", err)
	}
	return parseTriageJSON([]byte(chat.Message.Content))
}
