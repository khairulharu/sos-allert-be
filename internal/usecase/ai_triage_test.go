package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang-clean-architecture/internal/entity"
)

// fakeTriageEngine is a scripted EmergencyTriage for usecase tests.
type fakeTriageEngine struct {
	res  TriageResult
	err  error
	call []TriageInput
}

func (f *fakeTriageEngine) Triage(_ context.Context, in TriageInput) (TriageResult, error) {
	f.call = append(f.call, in)
	return f.res, f.err
}

func TestOfflineClassifier(t *testing.T) {
	cases := []struct {
		name         string
		desc         string
		wantType     string
		wantSeverity float64
		wantSummary  bool
	}{
		{name: "empty description is no signal", desc: "", wantSeverity: 0},
		{name: "unknown text is no signal", desc: "mau beli kopi dulu", wantSeverity: 0},
		{name: "medical faint", desc: "ada orang pingsan di pantai", wantType: entity.EmergencyTypeMedical, wantSeverity: 0.6, wantSummary: true},
		{name: "medical heart", desc: "bapak saya sakit jantung, sesak napas", wantType: entity.EmergencyTypeMedical, wantSeverity: 0.6, wantSummary: true},
		{name: "accident", desc: "kecelakaan motor di jalan raya", wantType: entity.EmergencyTypeAccident, wantSeverity: 0.6, wantSummary: true},
		{name: "danger", desc: "diculik orang tak dikenal", wantType: entity.EmergencyTypeDanger, wantSeverity: 0.6, wantSummary: true},
		{name: "danger urgent", desc: "diancam pakai pisau, darurat!", wantType: entity.EmergencyTypeDanger, wantSeverity: 0.8, wantSummary: true},
		{name: "critical accident", desc: "tabrakan beruntun, banyak korban", wantType: entity.EmergencyTypeAccident, wantSeverity: 0.8, wantSummary: true},
		{name: "english medical", desc: "someone is bleeding, unconscious", wantType: entity.EmergencyTypeMedical, wantSeverity: 0.8, wantSummary: true},
		{name: "uppercase", desc: "KECELAKAAN DI JALAN", wantType: entity.EmergencyTypeAccident, wantSeverity: 0.6, wantSummary: true},
		{name: "medical wins tie by life-safety priority", desc: "kecelakaan, teman saya pingsan", wantType: entity.EmergencyTypeMedical, wantSeverity: 0.6, wantSummary: true},
		{name: "severity capped at one", desc: "kecelakaan motor, diancam, darurat, kritis, banyak korban", wantType: entity.EmergencyTypeAccident, wantSeverity: 1.0, wantSummary: true},
		// Expanded Indonesian coverage: disasters map to DANGER because the
		// request model only accepts DANGER/MEDICAL/ACCIDENT.
		{name: "house fire", desc: "kebakaran di rumah tetangga", wantType: entity.EmergencyTypeDanger, wantSeverity: 0.8, wantSummary: true},
		{name: "flash flood", desc: "banjir bandang di kampung kami", wantType: entity.EmergencyTypeDanger, wantSeverity: 0.8, wantSummary: true},
		{name: "earthquake", desc: "gempa bumi, plafon rumah retak", wantType: entity.EmergencyTypeDanger, wantSeverity: 0.8, wantSummary: true},
		{name: "drowning", desc: "anak tenggelam di sungai", wantType: entity.EmergencyTypeMedical, wantSeverity: 0.8, wantSummary: true},
		{name: "colloquial seizure", desc: "bapak kejang-kejang, hampir tidak sadarkan diri", wantType: entity.EmergencyTypeMedical, wantSeverity: 1.0, wantSummary: true},
		{name: "stretched typing", desc: "tolonggg, anak saya pingsannn", wantType: entity.EmergencyTypeMedical, wantSeverity: 0.6, wantSummary: true},
		{name: "transit harassment", desc: "dicolek orang di kereta", wantType: entity.EmergencyTypeDanger, wantSeverity: 0.6, wantSummary: true},
		{name: "mugging", desc: "tetangga dibacok pencuri", wantType: entity.EmergencyTypeDanger, wantSeverity: 0.6, wantSummary: true},
		{name: "overturned truck", desc: "truk terbalik masuk jurang", wantType: entity.EmergencyTypeAccident, wantSeverity: 0.6, wantSummary: true},
		{name: "suffix tolerance", desc: "kecelakaannya banyak, korban luka", wantType: entity.EmergencyTypeAccident, wantSeverity: 0.8, wantSummary: true},
		// False-positive guards: short roots must not match inside
		// unrelated everyday words.
		{name: "meeting time is not an attack", desc: "besok rapat pukul 10 pagi", wantSeverity: 0},
		{name: "fruit is not arson", desc: "saya makan grape dan apel", wantSeverity: 0},
		{name: "power outage is not a fire", desc: "mati lampu di rumah", wantSeverity: 0},
	}

	c := NewOfflineClassifier()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := c.Triage(context.Background(), TriageInput{Description: tc.desc})
			if err != nil {
				t.Fatalf("Triage() error = %v", err)
			}
			if res.EmergencyType != tc.wantType {
				t.Errorf("EmergencyType = %q, want %q", res.EmergencyType, tc.wantType)
			}
			if res.Severity != tc.wantSeverity {
				t.Errorf("Severity = %v, want %v", res.Severity, tc.wantSeverity)
			}
			if tc.wantSummary && res.Summary == "" {
				t.Errorf("Summary = %q, want non-empty", res.Summary)
			}
			if !tc.wantSummary && res.Summary != "" {
				t.Errorf("Summary = %q, want empty", res.Summary)
			}
			if tc.wantSeverity >= 0.8 && res.Summary != "" && !strings.HasPrefix(res.Summary, "PRIORITAS TINGGI: ") {
				t.Errorf("Summary = %q, want PRIORITAS TINGGI prefix", res.Summary)
			}
		})
	}
}

func TestOfflineClassifierSummaryEvidence(t *testing.T) {
	res, err := NewOfflineClassifier().Triage(context.Background(), TriageInput{Description: "kebakaran di rumah tetangga"})
	if err != nil {
		t.Fatalf("Triage() error = %v", err)
	}
	if !strings.Contains(res.Summary, "kebakaran") {
		t.Errorf("Summary = %q, want matched evidence %q", res.Summary, "kebakaran")
	}
}

func TestOfflineClassifierTokenMatching(t *testing.T) {
	c := NewOfflineClassifier()
	// Whole-suffix matching only: keywords must not match inside a
	// longer, unrelated word.
	if res, _ := c.Triage(context.Background(), TriageInput{Description: "motorik halus anak saya bagus"}); res.EmergencyType != "" {
		t.Errorf("motorik classified as %q, want no signal", res.EmergencyType)
	}
	if res, _ := c.Triage(context.Background(), TriageInput{Description: "grape juice enak"}); res.EmergencyType != "" {
		t.Errorf("grape classified as %q, want no signal", res.EmergencyType)
	}
}

func TestAITriageFallback(t *testing.T) {
	healthy := &fakeTriageEngine{res: TriageResult{EmergencyType: entity.EmergencyTypeMedical, Severity: 0.8, Summary: "sakit"}}
	offline := &fakeTriageEngine{res: TriageResult{EmergencyType: entity.EmergencyTypeAccident, Severity: 0.6, Summary: "kecelakaan"}}

	t.Run("live result wins when provider is healthy", func(t *testing.T) {
		eng := NewAITriage(noopLogger(), healthy, offline)
		res, err := eng.Triage(context.Background(), TriageInput{Description: "x"})
		if err != nil {
			t.Fatalf("Triage() error = %v", err)
		}
		if res.EmergencyType != healthy.res.EmergencyType {
			t.Errorf("EmergencyType = %q, want %q", res.EmergencyType, healthy.res.EmergencyType)
		}
		if len(offline.call) != 0 {
			t.Errorf("offline classifier called %d times, want 0", len(offline.call))
		}
	})

	t.Run("provider error falls back to offline", func(t *testing.T) {
		failing := &fakeTriageEngine{err: errors.New("provider down")}
		eng := NewAITriage(noopLogger(), failing, offline)
		res, err := eng.Triage(context.Background(), TriageInput{Description: "x"})
		if err != nil {
			t.Fatalf("Triage() error = %v", err)
		}
		if res != offline.res {
			t.Errorf("result = %+v, want offline %+v", res, offline.res)
		}
		if len(offline.call) != 1 {
			t.Errorf("offline classifier called %d times, want 1", len(offline.call))
		}
	})

	t.Run("nil provider uses offline directly", func(t *testing.T) {
		eng := NewAITriage(noopLogger(), nil, offline)
		res, err := eng.Triage(context.Background(), TriageInput{Description: "x"})
		if err != nil || res != offline.res {
			t.Errorf("result = %+v err = %v, want offline %+v", res, err, offline.res)
		}
	})

	t.Run("nil offline is fail-open zero", func(t *testing.T) {
		failing := &fakeTriageEngine{err: errors.New("provider down")}
		eng := NewAITriage(noopLogger(), failing, nil)
		res, err := eng.Triage(context.Background(), TriageInput{Description: "x"})
		if err != nil {
			t.Fatalf("Triage() must not fail the SOS, got %v", err)
		}
		if res != (TriageResult{}) {
			t.Errorf("result = %+v, want zero", res)
		}
	})
}

func TestAITriageChainOrder(t *testing.T) {
	jevDown := &fakeTriageEngine{err: errors.New("jev down")}
	openAI := &fakeTriageEngine{res: TriageResult{EmergencyType: entity.EmergencyTypeDanger, Severity: 0.9, Summary: "openai"}}
	offline := &fakeTriageEngine{res: TriageResult{EmergencyType: entity.EmergencyTypeAccident, Severity: 0.6, Summary: "offline"}}

	t.Run("first healthy engine wins without calling the rest", func(t *testing.T) {
		eng := NewAITriageChain(noopLogger(), jevDown, openAI, offline)
		res, err := eng.Triage(context.Background(), TriageInput{Description: "x"})
		if err != nil {
			t.Fatalf("Triage() error = %v", err)
		}
		if res != openAI.res {
			t.Errorf("result = %+v, want the OpenAI stage", res)
		}
		if len(offline.call) != 0 {
			t.Errorf("offline called %d times, want 0", len(offline.call))
		}
	})

	t.Run("all engines fail is fail-open zero", func(t *testing.T) {
		eng := NewAITriageChain(noopLogger(),
			&fakeTriageEngine{err: errors.New("jev down")},
			&fakeTriageEngine{err: errors.New("openai down")},
			&fakeTriageEngine{err: errors.New("offline down")})
		res, err := eng.Triage(context.Background(), TriageInput{Description: "x"})
		if err != nil {
			t.Fatalf("Triage() must not fail the SOS, got %v", err)
		}
		if res != (TriageResult{}) {
			t.Errorf("result = %+v, want zero", res)
		}
	})

	t.Run("nil engines are dropped", func(t *testing.T) {
		eng := NewAITriageChain(noopLogger(), nil, offline, nil)
		if len(eng.Chain) != 1 {
			t.Fatalf("chain length = %d, want 1", len(eng.Chain))
		}
		res, err := eng.Triage(context.Background(), TriageInput{Description: "x"})
		if err != nil || res != offline.res {
			t.Errorf("result = %+v err = %v, want offline", res, err)
		}
	})

	t.Run("empty chain is fail-open zero", func(t *testing.T) {
		res, err := NewAITriageChain(noopLogger()).Triage(context.Background(), TriageInput{Description: "x"})
		if err != nil || res != (TriageResult{}) {
			t.Errorf("result = %+v err = %v, want zero and no error", res, err)
		}
	})
}

func TestOllamaClientTriage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s, want /api/chat", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var body ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "llama3" {
			t.Errorf("model = %q, want llama3", body.Model)
		}
		if body.Format != "json" || body.Stream {
			t.Errorf("format/stream = %q/%v, want json/false", body.Format, body.Stream)
		}
		if len(body.Messages) != 2 || body.Messages[1].Content != "orang pingsan" {
			t.Errorf("messages = %+v, want system+user", body.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"{\"emergency_type\":\"MEDICAL\",\"severity\":0.8,\"summary\":\"Ada korban pingsan\"}"},"done":true}`))
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL, "llama3", time.Second)
	res, err := c.Triage(context.Background(), TriageInput{Description: "orang pingsan"})
	if err != nil {
		t.Fatalf("Triage() error = %v", err)
	}
	if res.EmergencyType != entity.EmergencyTypeMedical || res.Severity != 0.8 || res.Summary != "Ada korban pingsan" {
		t.Errorf("result = %+v, want MEDICAL/0.8", res)
	}
}

func TestOllamaClientTriageErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantErrSub string
	}{
		{name: "http error", status: http.StatusInternalServerError, body: `{"error":"boom"}`, wantErrSub: "status 500"},
		{name: "invalid json", status: http.StatusOK, body: `{"message":{"role":"assistant","content":"not json"}}`, wantErrSub: "invalid provider JSON"},
		{name: "unknown type", status: http.StatusOK, body: `{"message":{"role":"assistant","content":"{\"emergency_type\":\"FIRE\",\"severity\":0.5}"}}`, wantErrSub: "unknown emergency_type"},
		{name: "severity out of range", status: http.StatusOK, body: `{"message":{"role":"assistant","content":"{\"emergency_type\":\"MEDICAL\",\"severity\":7}"}}`, wantErrSub: "out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := NewOllamaClient(srv.URL, "llama3", 500*time.Millisecond)
			_, err := c.Triage(context.Background(), TriageInput{Description: "x"})
			if err == nil {
				t.Fatalf("Triage() error = nil, want error containing %q", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

func TestOpenAIClientTriage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want Bearer sk-test", got)
		}
		var body openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.ResponseFormat.Type != "json_object" || body.Temperature != 0 {
			t.Errorf("response_format/temperature = %+v/%v, want json_object/0", body.ResponseFormat, body.Temperature)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"emergency_type\":\"ACCIDENT\",\"severity\":0.9,\"summary\":\"Kecelakaan beruntun\"}"}}]}`))
	}))
	defer srv.Close()

	c := NewOpenAIClient(srv.URL, "sk-test", "gpt-4o-mini", time.Second)
	res, err := c.Triage(context.Background(), TriageInput{Description: "tabrakan"})
	if err != nil {
		t.Fatalf("Triage() error = %v", err)
	}
	if res.EmergencyType != entity.EmergencyTypeAccident || res.Severity != 0.9 || res.Summary != "Kecelakaan beruntun" {
		t.Errorf("result = %+v, want ACCIDENT/0.9", res)
	}
}

func TestOpenAIClientTriageErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":"invalid api key"}`},
		{name: "empty choices", status: http.StatusOK, body: `{"choices":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := NewOpenAIClient(srv.URL, "sk-test", "gpt-4o-mini", 500*time.Millisecond)
			if _, err := c.Triage(context.Background(), TriageInput{Description: "x"}); err == nil {
				t.Fatal("Triage() error = nil, want error")
			}
		})
	}
}
