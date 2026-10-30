package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func chatServer(t *testing.T, status int, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, content)
	}))
}

func TestHTTPChatProviderSuccess(t *testing.T) {
	payload := `{"category":"ACCIDENT_TRAFFIC","severity":5,"confidence":0.9,"language":"en","summary_id":"Kecelakaan lalu lintas: korban jatuh dari motor dan berdarah.","summary_en":"Traffic accident: victim fell off a motorbike and is bleeding.","keywords":["motorbike","bleeding"],"needs":["first-aid","ambulance"],"needs_clarification":false}`
	srv := chatServer(t, http.StatusOK, payload)
	defer srv.Close()
	p := &HTTPChatProvider{BaseURL: srv.URL, APIKey: "test-key", Model: "test-model", HTTPClient: srv.Client()}
	r, err := p.Complete(context.Background(), Input{Text: "fell off motorbike, bleeding", AreaLabel: "Canggu, Bali"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if r.Category != CategoryAccidentTraffic || r.Severity != 5 || r.Source != "llm" {
		t.Errorf("result = %+v", r)
	}
	if r.SummaryID == "" || r.SummaryEN == "" {
		t.Error("summaries must be preserved from the provider")
	}
}

func TestHTTPChatProviderFencedJSON(t *testing.T) {
	payload := "```json\n{\"category\":\"MEDICAL\",\"severity\":3,\"confidence\":0.6,\"language\":\"id\",\"summary_id\":\"Darurat medis.\",\"summary_en\":\"Medical emergency.\"}\n```"
	srv := chatServer(t, http.StatusOK, payload)
	defer srv.Close()
	p := &HTTPChatProvider{BaseURL: srv.URL, APIKey: "k", Model: "m", HTTPClient: srv.Client()}
	r, err := p.Complete(context.Background(), Input{Text: "sakit"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if r.Category != CategoryMedical {
		t.Errorf("category = %q", r.Category)
	}
}

func TestHTTPChatProviderFailures(t *testing.T) {
	srv := chatServer(t, http.StatusUnauthorized, "nope")
	defer srv.Close()
	p := &HTTPChatProvider{BaseURL: srv.URL, APIKey: "k", Model: "m", HTTPClient: srv.Client()}
	if _, err := p.Complete(context.Background(), Input{Text: "help"}); err == nil {
		t.Error("non-2xx status must return an error")
	}

	badJSON := chatServer(t, http.StatusOK, "not json at all")
	defer badJSON.Close()
	p2 := &HTTPChatProvider{BaseURL: badJSON.URL, APIKey: "k", Model: "m", HTTPClient: badJSON.Client()}
	if _, err := p2.Complete(context.Background(), Input{Text: "help"}); err == nil {
		t.Error("non-JSON output must return an error")
	}

	badCat := chatServer(t, http.StatusOK, `{"category":"ALIEN","severity":3,"confidence":0.9}`)
	defer badCat.Close()
	p3 := &HTTPChatProvider{BaseURL: badCat.URL, APIKey: "k", Model: "m", HTTPClient: badCat.Client()}
	if _, err := p3.Complete(context.Background(), Input{Text: "help"}); err == nil {
		t.Error("unknown category must return an error")
	}

	p4 := &HTTPChatProvider{}
	if _, err := p4.Complete(context.Background(), Input{Text: "help"}); err == nil {
		t.Error("unconfigured provider must return an error")
	}
}

type failingProvider struct{}

func (failingProvider) Complete(ctx context.Context, in Input) (Result, error) {
	return Result{}, errors.New("provider down")
}

func TestServiceFallsBackToRules(t *testing.T) {
	svc := NewService(failingProvider{}, nil)
	r, err := svc.Triage(context.Background(), Input{Text: "Tolong, ada orang tenggelam di pantai"})
	if err != nil {
		t.Fatalf("Triage() error = %v, want rule fallback", err)
	}
	if r.Source != "rules" || r.Category != CategoryDrowning {
		t.Errorf("fallback result = %+v", r)
	}
	if _, err := svc.Triage(context.Background(), Input{Text: "  "}); err == nil {
		t.Error("empty text must return an error")
	}
}

func TestServiceRuleOnly(t *testing.T) {
	svc := NewService(nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := svc.Triage(ctx, Input{Text: "Kebakaran!"})
	if err != nil {
		t.Fatalf("Triage() error = %v", err)
	}
	if r.Category != CategoryFire {
		t.Errorf("category = %q", r.Category)
	}
}
