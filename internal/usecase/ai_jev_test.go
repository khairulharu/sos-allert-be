package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang-clean-architecture/internal/entity"
)

// newJevTestServer serves a scripted TypeSafe response and records the
// decoded request for assertions.
func newJevTestServer(t *testing.T, status int, body string, captured *jevRequest, hits *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		if r.URL.Path != "/systemone" {
			t.Errorf("path = %s, want /systemone", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer jev-test" {
			t.Errorf("Authorization = %q, want Bearer jev-test", got)
		}
		if captured != nil {
			if err := json.NewDecoder(r.Body).Decode(captured); err != nil {
				t.Errorf("decode request: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestJevClientTriage(t *testing.T) {
	var req jevRequest
	srv := newJevTestServer(t, http.StatusOK, `{
		"model": "jev-1.14.0",
		"answers": {
			"emergency_type": {"type":"choice","choice":"MEDICAL","confidence":0.93,
				"probabilities":{"MEDICAL":0.93,"DANGER":0.05,"ACCIDENT":0.02}},
			"severity": {"type":"score","score":3.0,"confidence":0.8,
				"legend":{"0":"a","1":"b","2":"c","3":"d","4":"e"},
				"probabilities":{"0":0.0,"1":0.0,"2":0.0,"3":1.0,"4":0.0}}
		}
	}`, &req, nil)
	defer srv.Close()

	c := NewJevClient(srv.URL, "jev-test", "jev-latest", time.Second)
	res, err := c.Triage(context.Background(), TriageInput{Description: "bapak saya sesak napas"})
	if err != nil {
		t.Fatalf("Triage() error = %v", err)
	}
	if res.EmergencyType != entity.EmergencyTypeMedical {
		t.Errorf("EmergencyType = %q, want MEDICAL", res.EmergencyType)
	}
	// Score 3 on a five-level rubric (0..4) normalizes to 0.75.
	if res.Severity != 0.75 {
		t.Errorf("Severity = %v, want 0.75", res.Severity)
	}
	if !strings.Contains(res.Summary, "medis") {
		t.Errorf("Summary = %q, want the local Indonesian medical summary", res.Summary)
	}
	if strings.HasPrefix(res.Summary, "PRIORITAS TINGGI: ") {
		t.Errorf("Summary = %q, want no high-priority prefix below 0.8", res.Summary)
	}

	// Request shape: one System One call with both typed questions.
	if req.Model != "jev-latest" {
		t.Errorf("model = %q, want jev-latest", req.Model)
	}
	state, ok := req.State.(map[string]any)
	if !ok || state["message"] != "bapak saya sesak napas" {
		t.Errorf("state = %+v, want the description under message", req.State)
	}
	typeQ, ok := req.Questions[jevQuestionType]
	if !ok || typeQ.Type != "choice" {
		t.Fatalf("questions = %+v, want a choice emergency_type question", req.Questions)
	}
	criteria, ok := typeQ.Criteria.(map[string]any)
	if !ok || len(criteria) != 3 {
		t.Errorf("choice criteria = %+v, want 3 emergency types", typeQ.Criteria)
	}
	sevQ, ok := req.Questions[jevQuestionSeverity]
	if !ok || sevQ.Type != "score" {
		t.Fatalf("questions = %+v, want a score severity question", req.Questions)
	}
	levels, ok := sevQ.Criteria.([]any)
	if !ok || len(levels) != len(jevSeverityLevels) {
		t.Errorf("score criteria = %+v, want %d levels", sevQ.Criteria, len(jevSeverityLevels))
	}
}

func TestJevClientTriageCriticalSeverity(t *testing.T) {
	srv := newJevTestServer(t, http.StatusOK, `{
		"model": "jev-1.14.0",
		"answers": {
			"emergency_type": {"type":"choice","choice":"DANGER","confidence":0.99,"probabilities":{}},
			"severity": {"type":"score","score":4.0,"confidence":0.9}
		}
	}`, nil, nil)
	defer srv.Close()

	c := NewJevClient(srv.URL, "jev-test", "jev-latest", time.Second)
	res, err := c.Triage(context.Background(), TriageInput{Description: "kebakaran, banyak korban"})
	if err != nil {
		t.Fatalf("Triage() error = %v", err)
	}
	if res.EmergencyType != entity.EmergencyTypeDanger || res.Severity != 1.0 {
		t.Errorf("result = %+v, want DANGER/1.0", res)
	}
	if !strings.HasPrefix(res.Summary, "PRIORITAS TINGGI: ") {
		t.Errorf("Summary = %q, want high-priority prefix at severity 1.0", res.Summary)
	}
}

func TestJevClientTriageErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantErrSub string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"detail":"bad key"}`, wantErrSub: "status 401"},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"detail":"slow down"}`, wantErrSub: "status 429"},
		{name: "invalid json", status: http.StatusOK, body: `not json`, wantErrSub: "decode response"},
		{name: "missing emergency answer", status: http.StatusOK, body: `{"answers":{}}`, wantErrSub: "missing"},
		{name: "wrong answer type", status: http.StatusOK, body: `{"answers":{"emergency_type":{"type":"score","score":1}}}`,
			wantErrSub: "want choice"},
		{name: "unknown type", status: http.StatusOK, body: `{"answers":{"emergency_type":{"type":"choice","choice":"FIRE","confidence":0.9}}}`,
			wantErrSub: "unknown emergency_type"},
		{name: "missing severity answer", status: http.StatusOK, body: `{"answers":{"emergency_type":{"type":"choice","choice":"MEDICAL","confidence":0.9}}}`,
			wantErrSub: "missing"},
		{name: "severity out of range", status: http.StatusOK, body: `{"answers":{"emergency_type":{"type":"choice","choice":"MEDICAL","confidence":0.9},"severity":{"type":"score","score":9}}}`,
			wantErrSub: "out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newJevTestServer(t, tc.status, tc.body, nil, nil)
			defer srv.Close()

			c := NewJevClient(srv.URL, "jev-test", "jev-latest", 500*time.Millisecond)
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

func TestJevClientLowConfidenceFallsThrough(t *testing.T) {
	srv := newJevTestServer(t, http.StatusOK, `{
		"answers": {
			"emergency_type": {"type":"choice","choice":"MEDICAL","confidence":0.2,"probabilities":{}},
			"severity": {"type":"score","score":2.0}
		}
	}`, nil, nil)
	defer srv.Close()

	c := NewJevClient(srv.URL, "jev-test", "jev-latest", time.Second)
	_, err := c.Triage(context.Background(), TriageInput{Description: "ada yang jatuh"})
	if !errors.Is(err, errTriageLowConfidence) {
		t.Fatalf("error = %v, want errTriageLowConfidence", err)
	}
}

func TestJevClientSkipsEmptyDescriptionAndMissingKey(t *testing.T) {
	var hits int32
	srv := newJevTestServer(t, http.StatusOK, `{}`, nil, &hits)
	defer srv.Close()

	c := NewJevClient(srv.URL, "jev-test", "jev-latest", time.Second)
	res, err := c.Triage(context.Background(), TriageInput{Description: "   "})
	if err != nil || res != (TriageResult{}) {
		t.Errorf("empty description = %+v, %v; want zero result and no error", res, err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("server hits = %d, want 0 for an empty description", got)
	}

	noKey := NewJevClient(srv.URL, "", "jev-latest", time.Second)
	if _, err := noKey.Triage(context.Background(), TriageInput{Description: "x"}); err == nil {
		t.Error("missing api key: error = nil, want error")
	}
}

func TestJevClientDefaults(t *testing.T) {
	c := NewJevClient("", "", "", 0)
	if c.BaseURL != "https://api.typesafe.ai/v1" {
		t.Errorf("BaseURL = %q, want TypeSafe default", c.BaseURL)
	}
	if c.Model != "jev-latest" {
		t.Errorf("Model = %q, want jev-latest", c.Model)
	}
	if c.HTTP.Timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", c.HTTP.Timeout)
	}
}
