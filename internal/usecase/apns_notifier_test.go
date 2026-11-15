package usecase

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// apnsTestKeyPEM mints a throwaway ES256 key so the JWT path runs for real.
func apnsTestKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

// jwtCapture records the Authorization header of every APNs delivery.
type jwtCapture struct {
	mu     sync.Mutex
	tokens []string
}

func (c *jwtCapture) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens = append(c.tokens, r.Header.Get("Authorization"))
}

func (c *jwtCapture) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.tokens...)
}

// clock is a settable time source for the notifier.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func newAPNsTestNotifier(t *testing.T, baseURL string, now func() time.Time) *APNsNotifier {
	t.Helper()
	return &APNsNotifier{
		Enabled:       true,
		KeyID:         "KEY123",
		TeamID:        "TEAM123",
		BundleID:      "id.allert.app",
		PrivateKeyPEM: apnsTestKeyPEM(t),
		BaseURL:       baseURL,
		HTTPClient:    &http.Client{Timeout: 2 * time.Second},
		Now:           now,
	}
}

// One fan-out to several iOS devices must reuse a single provider token:
// minting one JWT per device is what made Apple answer 403
// TooManyProviderTokenUpdates (#42).
func TestAPNsReusesProviderTokenAcrossRecipients(t *testing.T) {
	capture := &jwtCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := newAPNsTestNotifier(t, server.URL, time.Now)
	for _, token := range []string{"device-1", "device-2", "device-3"} {
		if err := notifier.Send(context.Background(), token, "SOS", "Darurat", "alert-1"); err != nil {
			t.Fatalf("Send(%s) error = %v", token, err)
		}
	}

	tokens := capture.all()
	if len(tokens) != 3 {
		t.Fatalf("deliveries = %d, want 3", len(tokens))
	}
	for i, got := range tokens {
		if got == "" {
			t.Fatalf("delivery %d has no Authorization header", i)
		}
		if got != tokens[0] {
			t.Fatalf("delivery %d used a different provider token, want the cached one", i)
		}
	}
}

// The cache is time-bounded: after ~50 minutes the next push mints a new
// token instead of reusing one that is close to its exp.
func TestAPNsRefreshesProviderTokenAfterTTL(t *testing.T) {
	capture := &jwtCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	clk := &clock{at: time.Now()}
	notifier := newAPNsTestNotifier(t, server.URL, clk.now)

	if err := notifier.Send(context.Background(), "device-1", "SOS", "Darurat", "alert-1"); err != nil {
		t.Fatalf("first Send error = %v", err)
	}
	clk.advance(apnsProviderTokenTTL - time.Minute)
	if err := notifier.Send(context.Background(), "device-1", "SOS", "Darurat", "alert-1"); err != nil {
		t.Fatalf("second Send error = %v", err)
	}
	clk.advance(2 * time.Minute) // now past the refresh window
	if err := notifier.Send(context.Background(), "device-1", "SOS", "Darurat", "alert-1"); err != nil {
		t.Fatalf("third Send error = %v", err)
	}

	tokens := capture.all()
	if len(tokens) != 3 {
		t.Fatalf("deliveries = %d, want 3", len(tokens))
	}
	if tokens[0] != tokens[1] {
		t.Fatal("token inside the reuse window was re-minted")
	}
	if tokens[2] == tokens[1] {
		t.Fatal("token past the reuse window was not refreshed")
	}
}

// A failed mint must not be cached, so the next attempt can succeed.
func TestAPNsDoesNotCacheFailedToken(t *testing.T) {
	notifier := &APNsNotifier{Enabled: true, KeyID: "K", TeamID: "T", BundleID: "id.app", PrivateKeyPEM: []byte("not pem")}
	if _, err := notifier.providerToken(); err == nil {
		t.Fatal("providerToken with a broken key = nil, want an error")
	}
	if notifier.cachedToken != "" {
		t.Fatal("a failed mint was cached")
	}
}
