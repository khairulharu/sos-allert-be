package usecase

import (
	"context"

	"github.com/sirupsen/logrus"
)

// TriageInput is what the SOS trigger hands to the AI triage engine.
// Only the sender's free-text description is used; everything else
// (coordinates, battery, accuracy) stays out so triage stays cheap
// and deterministic.
type TriageInput struct {
	Description string
}

// TriageResult is the AI triage output. Severity is a 0..1 score that
// the trigger maps onto the configured adaptive fan-out radii (more
// severe incidents start broader). EmergencyType is "" when the engine
// has no signal, in which case the caller keeps its own default.
type TriageResult struct {
	EmergencyType string
	Severity      float64
	Summary       string
}

// EmergencyTriage classifies an SOS description into an emergency type,
// severity and a short summary. Fail-open contract: a nil engine, a
// disabled engine or an error must never block a life-safety alert.
type EmergencyTriage interface {
	Triage(ctx context.Context, in TriageInput) (TriageResult, error)
}

// AITriage is the composed triage engine: an optional live LLM provider
// (Ollama/OpenAI) with the always-available offline keyword classifier
// as fallback. A failing provider is logged and degrades gracefully, so
// the SOS trigger never depends on AI being up.
type AITriage struct {
	Log     *logrus.Logger
	LLM     EmergencyTriage // optional live provider; nil = offline only
	Offline EmergencyTriage // deterministic fallback
}

// NewAITriage builds the composed engine. offline may be nil, in which
// case a failing/absent LLM yields a zero result (caller keeps defaults).
func NewAITriage(log *logrus.Logger, llm EmergencyTriage, offline EmergencyTriage) *AITriage {
	return &AITriage{Log: log, LLM: llm, Offline: offline}
}

func (t *AITriage) Triage(ctx context.Context, in TriageInput) (TriageResult, error) {
	if t.LLM != nil {
		res, err := t.LLM.Triage(ctx, in)
		if err == nil {
			return res, nil
		}
		if t.Log != nil {
			t.Log.WithError(err).Warn("AI triage provider failed, falling back to offline classifier")
		} else if t.Offline == nil {
			return TriageResult{}, err
		}
	}
	if t.Offline != nil {
		return t.Offline.Triage(ctx, in)
	}
	return TriageResult{}, nil
}
