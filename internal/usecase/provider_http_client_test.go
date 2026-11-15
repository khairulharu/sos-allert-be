package usecase

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A notifier built without an injected client must not fall back to
// http.DefaultClient (Timeout 0): one stalled provider would hang the caller
// forever (#42).
func TestNotifiersUseBoundedDefaultClient(t *testing.T) {
	if ProviderHTTPTimeout <= 0 {
		t.Fatalf("ProviderHTTPTimeout = %v, want a positive bound", ProviderHTTPTimeout)
	}
	cases := map[string]*http.Client{
		"fcm":     (&FCMNotifier{}).client(),
		"apns":    (&APNsNotifier{}).client(),
		"twilio":  (&TwilioNotifier{}).client(),
		"webpush": (&WebPushNotifier{}).client(),
	}
	for name, client := range cases {
		if client == nil {
			t.Fatalf("%s client = nil, want the bounded default", name)
		}
		if client.Timeout != ProviderHTTPTimeout {
			t.Fatalf("%s client timeout = %v, want %v", name, client.Timeout, ProviderHTTPTimeout)
		}
		if client == http.DefaultClient {
			t.Fatalf("%s client is http.DefaultClient (no timeout)", name)
		}
	}
}

// An injected client keeps winning, so operators can still tune a provider.
func TestNotifiersPreferInjectedClient(t *testing.T) {
	injected := &http.Client{Timeout: 42 * time.Second}
	if got := (&FCMNotifier{HTTPClient: injected}).client(); got != injected {
		t.Fatalf("fcm client = %p, want the injected one", got)
	}
	if got := (&APNsNotifier{HTTPClient: injected}).client(); got != injected {
		t.Fatalf("apns client = %p, want the injected one", got)
	}
	if got := (&TwilioNotifier{HTTPClient: injected}).client(); got != injected {
		t.Fatalf("twilio client = %p, want the injected one", got)
	}
	if got := (&WebPushNotifier{HTTPClient: injected}).client(); got != injected {
		t.Fatalf("webpush client = %p, want the injected one", got)
	}
}

// The bound is real: a provider that stalls makes the request fail with a
// deadline instead of hanging. Uses a tiny timeout so the test stays fast.
func TestStalledProviderRequestTimesOut(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	notifier := &FCMNotifier{
		Enabled:     true,
		ProjectID:   "project",
		AccessToken: "token",
		BaseURL:     server.URL,
		HTTPClient:  &http.Client{Timeout: 30 * time.Millisecond},
	}
	start := time.Now()
	err := notifier.SendToToken(t.Context(), "device-token", "SOS", "body", nil)
	if err == nil {
		t.Fatal("SendToToken = nil, want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("stalled request took %v, want it bounded by the client timeout", elapsed)
	}
}
