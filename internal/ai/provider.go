package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// LLMProvider is the optional hosted-model backend. It speaks the
// OpenAI-compatible chat-completions shape so the operator can point
// it at any compatible provider by changing config only. The
// provider receives report text and an area label, never precise
// coordinates or user history.
type LLMProvider interface {
	Complete(ctx context.Context, in Input) (Result, error)
}

// HTTPChatProvider calls a chat-completions endpoint with a strict
// JSON-only instruction. It is deliberately small: one short call
// per triage, temperature 0, and a hard caller-supplied timeout
// (the use case caps it at a few seconds so SOS never waits on AI).
type HTTPChatProvider struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	ResponseFormat any           `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// llmTriagePayload is the exact JSON shape the model must return.
type llmTriagePayload struct {
	Category           string   `json:"category"`
	Severity           int      `json:"severity"`
	Confidence         float64  `json:"confidence"`
	Language           string   `json:"language"`
	SummaryID          string   `json:"summary_id"`
	SummaryEN          string   `json:"summary_en"`
	Keywords           []string `json:"keywords"`
	Needs              []string `json:"needs"`
	NeedsClarification bool     `json:"needs_clarification"`
}

const triageSystemPrompt = `You are an emergency triage assistant for Bali, Indonesia. ` +
	`Classify a short panic report into exactly one category: ACCIDENT_TRAFFIC, MEDICAL, DROWNING, FIRE, DANGER, NATURAL_DISASTER, or OTHER. ` +
	`Severity is 1 (low) to 5 (critical). Be conservative: when unsure, prefer a higher severity and lower confidence. ` +
	`Write summary_id in Indonesian and summary_en in English, each one short sentence, using only facts stated in the report. Never invent a name, exact address, age, or injury that was not stated. ` +
	`Respond with JSON only, no markdown, with keys: category, severity, confidence (0 to 1), language (ISO code or "unknown"), summary_id, summary_en, keywords (max 5), needs (short tags such as first-aid, ambulance, lifeguard, fire-service, police), needs_clarification (true when the report is too vague).`

// Complete sends one triage request. Any transport, status, or parse
// failure is returned so the caller can fall back to the rule
// classifier; Complete itself never invents a result.
func (p *HTTPChatProvider) Complete(ctx context.Context, in Input) (Result, error) {
	if p.BaseURL == "" || p.APIKey == "" || p.Model == "" {
		return Result{}, fmt.Errorf("AI provider is not configured")
	}
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	userContent := "Report: " + in.Text
	if area := strings.TrimSpace(in.AreaLabel); area != "" {
		userContent += "\nArea (approximate, not an exact address): " + area
	}
	body, err := json.Marshal(chatRequest{
		Model:       p.Model,
		Messages:    []chatMessage{{Role: "system", Content: triageSystemPrompt}, {Role: "user", Content: userContent}},
		Temperature: 0,
		ResponseFormat: map[string]string{
			"type": "json_object",
		},
	})
	if err != nil {
		return Result{}, err
	}
	url := strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("AI provider request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("AI provider returned status %d", resp.StatusCode)
	}
	var decoded chatResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Result{}, fmt.Errorf("AI provider response is not valid chat JSON: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return Result{}, fmt.Errorf("AI provider returned no choices")
	}
	content := strings.TrimSpace(decoded.Choices[0].Message.Content)
	payload, err := parseTriageJSON(content)
	if err != nil {
		return Result{}, err
	}
	r := Result{
		Category:           payload.Category,
		Severity:           payload.Severity,
		Confidence:         payload.Confidence,
		Language:           payload.Language,
		SummaryID:          payload.SummaryID,
		SummaryEN:          payload.SummaryEN,
		Keywords:           payload.Keywords,
		Needs:              payload.Needs,
		NeedsClarification: payload.NeedsClarification,
		Source:             "llm",
	}
	return Validate(r), nil
}

// parseTriageJSON extracts the JSON object from model output,
// tolerating surrounding markdown fences or prose.
func parseTriageJSON(content string) (llmTriagePayload, error) {
	var payload llmTriagePayload
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end < 0 || end <= start {
		return payload, fmt.Errorf("AI output contains no JSON object")
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &payload); err != nil {
		return payload, fmt.Errorf("AI output JSON is invalid: %w", err)
	}
	if !ValidCategory(payload.Category) {
		return payload, fmt.Errorf("AI output category %q is not recognised", payload.Category)
	}
	return payload, nil
}

// Service is the Triager used by the SOS use case: LLM first when a
// provider is configured, rule classifier as the always-available
// fallback. Triage never returns an error for a provider failure;
// it returns the rule result instead, so callers never block an SOS
// on AI. It only errors when there is no text to classify at all.
type Service struct {
	Provider LLMProvider
	Log      *logrus.Logger
}

// NewService builds a triage service. A nil provider means
// rule-only operation (the demo/offline mode).
func NewService(provider LLMProvider, log *logrus.Logger) *Service {
	return &Service{Provider: provider, Log: log}
}

// Triage classifies the input, preferring the LLM provider.
func (s *Service) Triage(ctx context.Context, in Input) (Result, error) {
	if strings.TrimSpace(in.Text) == "" {
		return Result{}, fmt.Errorf("triage needs report text")
	}
	if s.Provider != nil {
		if r, err := s.Provider.Complete(ctx, in); err == nil {
			return r, nil
		} else if s.Log != nil {
			s.Log.WithError(err).Warn("AI provider failed, using rule-based triage fallback")
		}
	}
	return Classifier{}.Classify(in.Text), nil
}
