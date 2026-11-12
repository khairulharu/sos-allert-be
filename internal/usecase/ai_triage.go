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

// AITriage is the composed triage engine: an ordered chain of
// classifiers, typically Jev/OpenAI/offline or Ollama/offline. The first
// engine that returns a usable answer wins; a failing or low-confidence
// engine is logged and the next one is tried, so the SOS trigger never
// depends on any single AI provider being up.
type AITriage struct {
	Log   *logrus.Logger
	Chain []EmergencyTriage // tried in order; nil entries are dropped
}

// NewAITriage builds a two-stage engine (live provider + offline
// fallback). It is the backwards-compatible shorthand for
// NewAITriageChain; either argument may be nil.
func NewAITriage(log *logrus.Logger, llm EmergencyTriage, offline EmergencyTriage) *AITriage {
	return NewAITriageChain(log, llm, offline)
}

// NewAITriageChain builds the composed engine from an ordered list of
// classifiers, e.g. jev, then openai, then the offline keyword
// classifier. Nil engines are skipped. With an empty chain Triage is a
// zero result, which makes the caller keep its own defaults.
func NewAITriageChain(log *logrus.Logger, engines ...EmergencyTriage) *AITriage {
	chain := make([]EmergencyTriage, 0, len(engines))
	for _, engine := range engines {
		if engine != nil {
			chain = append(chain, engine)
		}
	}
	return &AITriage{Log: log, Chain: chain}
}

// Triage walks the chain and returns the first successful result. It
// always fails open: when every engine errors (or the chain is empty)
// the result is zero and the error is nil, so a life-safety alert still
// goes out with the request/default classification.
func (t *AITriage) Triage(ctx context.Context, in TriageInput) (TriageResult, error) {
	for i, engine := range t.Chain {
		res, err := engine.Triage(ctx, in)
		if err == nil {
			return res, nil
		}
		if t.Log != nil {
			t.Log.WithError(err).Warnf("AI triage stage %d/%d failed, trying the next classifier", i+1, len(t.Chain))
		}
	}
	return TriageResult{}, nil
}
