package usecase

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"golang-clean-architecture/internal/entity"
)

// An SOS without a description must not reach a paid provider: OpenAI and
// Ollama answer a blank prompt locally with a zero result (#42). The request
// counter proves no HTTP call is made, not just that the result is empty.
func TestTriageProvidersSkipEmptyDescription(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	openAI := NewOpenAIClient(server.URL, "sk-test", "gpt-4o-mini", time.Second)
	ollama := NewOllamaClient(server.URL, "llama3", time.Second)

	for _, description := range []string{"", "   ", "\n\t "} {
		for name, engine := range map[string]EmergencyTriage{"openai": openAI, "ollama": ollama} {
			res, err := engine.Triage(context.Background(), TriageInput{Description: description})
			if err != nil {
				t.Fatalf("%s Triage(%q) error = %v, want a silent zero result", name, description, err)
			}
			if res != (TriageResult{}) {
				t.Fatalf("%s Triage(%q) = %+v, want zero", name, description, res)
			}
		}
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("provider HTTP calls = %d, want 0 for empty descriptions", got)
	}
}

// A blank description through the whole chain (jev -> openai -> offline) must
// still cost nothing: every stage answers locally.
func TestTriageChainEmptyDescriptionDoesNotCallProviders(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	chain := NewAITriageChain(noopLogger(),
		NewJevClient(server.URL, "model", "key", time.Second),
		NewOpenAIClient(server.URL, "sk-test", "gpt-4o-mini", time.Second),
		NewOllamaClient(server.URL, "llama3", time.Second),
		NewOfflineClassifier(),
	)
	res, err := chain.Triage(context.Background(), TriageInput{Description: " "})
	if err != nil {
		t.Fatalf("Triage error = %v", err)
	}
	if res != (TriageResult{}) {
		t.Fatalf("result = %+v, want zero", res)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("provider HTTP calls = %d, want 0", got)
	}
}

// A stage that answers successfully with no signal is not an answer: the
// chain must keep walking so the offline classifier can still classify.
func TestTriageChainFallsThroughOnZeroSignal(t *testing.T) {
	noSignal := &fakeTriageEngine{} // zero result, nil error
	offline := &fakeTriageEngine{res: TriageResult{
		EmergencyType: entity.EmergencyTypeMedical, Severity: 0.7, Summary: "pingsan",
	}}

	res, err := NewAITriageChain(noopLogger(), noSignal, offline).Triage(context.Background(),
		TriageInput{Description: "orang pingsan"})
	if err != nil {
		t.Fatalf("Triage error = %v", err)
	}
	if res != offline.res {
		t.Fatalf("result = %+v, want the offline stage %+v", res, offline.res)
	}
	if len(noSignal.call) != 1 || len(offline.call) != 1 {
		t.Fatalf("calls = %d/%d, want both stages to run once", len(noSignal.call), len(offline.call))
	}
}

// When every stage is silent the result stays zero (fail-open, no error).
func TestTriageChainAllStagesSilentIsZero(t *testing.T) {
	res, err := NewAITriageChain(noopLogger(), &fakeTriageEngine{}, &fakeTriageEngine{}).
		Triage(context.Background(), TriageInput{Description: "mau beli kopi"})
	if err != nil {
		t.Fatalf("Triage error = %v", err)
	}
	if res != (TriageResult{}) {
		t.Fatalf("result = %+v, want zero", res)
	}
}
