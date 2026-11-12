package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang-clean-architecture/internal/entity"
)

// TypeSafe/Jev limits and thresholds.
const (
	// jevMinConfidence is the Choice confidence below which the answer
	// is treated as "no clear signal". TypeSafe recommends falling back
	// to a different system on low confidence, so the composed engine
	// then tries the next provider instead of acting on a coin flip.
	jevMinConfidence = 0.5
	// jevMaxErrorBodyBytes bounds how much of an error response we keep
	// for the log/error message.
	jevMaxErrorBodyBytes = 4096
)

// Jev question ids. Jev returns answers keyed by these names.
const (
	jevQuestionType     = "emergency_type"
	jevQuestionSeverity = "severity"
)

// Jev severity rubric: five ordered levels, low to high. The Score
// answer is the probability-weighted position on this scale (0..4), so
// severity = score / (len-1).
var jevSeverityLevels = []string{
	"No emergency: informational message, no danger",
	"Minor: uncomfortable or needs help, but not life-threatening right now",
	"Moderate: needs a fast response; the condition may get worse",
	"High: life-threatening, needs help immediately",
	"Critical: imminent death, not breathing or cardiac arrest, active violence, many victims",
}

// Jev emergency taxonomy. The keys are the exact values stored in
// alerts.emergency_type, so no translation step is needed.
var jevEmergencyChoices = map[string]string{
	entity.EmergencyTypeDanger: "Immediate threat to safety: crime (mugging, robbery, kidnapping), " +
		"violence or assault, being chased, sexual violence, fire, natural disasters " +
		"(earthquake, flood, landslide), dangerous animals, or a weapon.",
	entity.EmergencyTypeMedical: "Medical emergency: unconscious or unresponsive, trouble or stopped " +
		"breathing, chest pain or cardiac arrest, seizure, heavy bleeding, poisoning, " +
		"drowning, pregnancy or miscarriage, severe injury, or a severe allergic reaction.",
	entity.EmergencyTypeAccident: "Accident: vehicle collision, fall from height, vehicle went off the " +
		"road, crushed or trapped, overturned vehicle, brake failure, or a workplace accident.",
}

// errTriageLowConfidence marks a Jev answer the model itself is unsure
// about. The composed engine treats it like any other provider error and
// moves to the next classifier in the chain.
var errTriageLowConfidence = errors.New("ai triage: low confidence")

// JevClient classifies via TypeSafe's System One endpoint
// (POST /v1/systemone). Jev does not generate text: it answers typed
// questions, so the SOS emergency type is a Choice, the 0..1 severity is
// a normalized Score, and the Indonesian summary is derived locally from
// the typed answers (see summarizeTriage).
type JevClient struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// NewJevClient builds the client. TypeSafe defaults are applied when the
// fields are empty; a non-positive timeout defaults to 5s so a hung
// request cannot stall the SOS flow.
func NewJevClient(baseURL, apiKey, model string, timeout time.Duration) *JevClient {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if baseURL == "" {
		baseURL = "https://api.typesafe.ai/v1"
	}
	if model == "" {
		model = "jev-latest"
	}
	return &JevClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

type jevRequest struct {
	Model     string                 `json:"model"`
	State     any                    `json:"state"`
	Questions map[string]jevQuestion `json:"questions"`
}

type jevQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions,omitempty"`
	Criteria     any    `json:"criteria,omitempty"`
}

type jevAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Score         float64            `json:"score"`
	Legend        map[string]string  `json:"legend"`
}

type jevResponse struct {
	Model   string               `json:"model"`
	Answers map[string]jevAnswer `json:"answers"`
}

func (c *JevClient) Triage(ctx context.Context, in TriageInput) (TriageResult, error) {
	if strings.TrimSpace(in.Description) == "" {
		return TriageResult{}, nil
	}
	if c.APIKey == "" {
		return TriageResult{}, fmt.Errorf("ai triage jev: api key is not configured")
	}

	// One request, two questions: Jev evaluates them in parallel against
	// the same state, so batching costs no extra latency.
	body := jevRequest{
		Model: c.Model,
		State: map[string]any{
			"message":  in.Description,
			"language": "id",
		},
		Questions: map[string]jevQuestion{
			jevQuestionType: {
				Type:         "choice",
				Instructions: "Which emergency category best fits this SOS message?",
				Criteria:     jevEmergencyChoices,
			},
			jevQuestionSeverity: {
				Type:         "score",
				Instructions: "How life-threatening and time-critical is this situation right now?",
				Criteria:     jevSeverityLevels,
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return TriageResult{}, fmt.Errorf("ai triage jev: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/systemone", bytes.NewReader(raw))
	if err != nil {
		return TriageResult{}, fmt.Errorf("ai triage jev: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return TriageResult{}, fmt.Errorf("ai triage jev: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, jevMaxErrorBodyBytes))
		return TriageResult{}, fmt.Errorf("ai triage jev: unexpected status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(detail)))
	}

	var out jevResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return TriageResult{}, fmt.Errorf("ai triage jev: decode response: %w", err)
	}
	return parseJevAnswers(out.Answers)
}

// parseJevAnswers validates the typed answers and maps them onto the
// SOS triage shape. Any structural surprise is an error so the composed
// engine can fall back to the next provider.
func parseJevAnswers(answers map[string]jevAnswer) (TriageResult, error) {
	typeAnswer, ok := answers[jevQuestionType]
	if !ok {
		return TriageResult{}, fmt.Errorf("ai triage jev: missing %q answer", jevQuestionType)
	}
	if typeAnswer.Type != "choice" {
		return TriageResult{}, fmt.Errorf("ai triage jev: %q answer type %q, want choice", jevQuestionType, typeAnswer.Type)
	}
	if _, ok := jevEmergencyChoices[typeAnswer.Choice]; !ok {
		return TriageResult{}, fmt.Errorf("ai triage jev: unknown emergency_type %q", typeAnswer.Choice)
	}
	if typeAnswer.Confidence < jevMinConfidence {
		return TriageResult{}, fmt.Errorf("%w: %s confidence %.2f", errTriageLowConfidence, jevQuestionType, typeAnswer.Confidence)
	}

	severityAnswer, ok := answers[jevQuestionSeverity]
	if !ok {
		return TriageResult{}, fmt.Errorf("ai triage jev: missing %q answer", jevQuestionSeverity)
	}
	if severityAnswer.Type != "score" {
		return TriageResult{}, fmt.Errorf("ai triage jev: %q answer type %q, want score", jevQuestionSeverity, severityAnswer.Type)
	}
	maxScore := float64(len(jevSeverityLevels) - 1)
	if severityAnswer.Score < 0 || severityAnswer.Score > maxScore {
		return TriageResult{}, fmt.Errorf("ai triage jev: severity score %v out of range 0..%v", severityAnswer.Score, maxScore)
	}
	severity := severityAnswer.Score / maxScore

	return TriageResult{
		EmergencyType: typeAnswer.Choice,
		Severity:      severity,
		Summary:       summarizeTriage(typeAnswer.Choice, severity, ""),
	}, nil
}
