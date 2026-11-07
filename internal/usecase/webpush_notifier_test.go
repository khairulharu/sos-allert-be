package usecase

import (
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang-clean-architecture/internal/entity"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/sirupsen/logrus"
)

// fakePushStore is an in-memory PushSubscriptionStore.
type fakePushStore struct {
	mu      sync.Mutex
	subs    map[string][]*entity.PushSubscription
	deleted []string
}

func newFakePushStore() *fakePushStore {
	return &fakePushStore{subs: map[string][]*entity.PushSubscription{}}
}

func (f *fakePushStore) Upsert(ctx context.Context, sub *entity.PushSubscription) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.subs[sub.UserID]
	for i, row := range rows {
		if row.Endpoint == sub.Endpoint {
			rows[i] = sub
			return nil
		}
	}
	f.subs[sub.UserID] = append(rows, sub)
	return nil
}

func (f *fakePushStore) ListByUserID(ctx context.Context, userID string) ([]*entity.PushSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subs[userID], nil
}

func (f *fakePushStore) DeleteByUserAndEndpoint(ctx context.Context, userID, endpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.subs[userID]
	for i, row := range rows {
		if row.Endpoint == endpoint {
			f.subs[userID] = append(rows[:i], rows[i+1:]...)
			return nil
		}
	}
	return nil
}

func (f *fakePushStore) DeleteByEndpoint(ctx context.Context, endpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, endpoint)
	for userID, rows := range f.subs {
		for i, row := range rows {
			if row.Endpoint == endpoint {
				f.subs[userID] = append(rows[:i], rows[i+1:]...)
				break
			}
		}
	}
	return nil
}

func (f *fakePushStore) deletedEndpoints() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

// capturedPush is one request the fake push service received.
type capturedPush struct {
	header http.Header
	body   []byte
}

type fakePushService struct {
	mu       sync.Mutex
	requests []capturedPush
	status   int
}

func (s *fakePushService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, capturedPush{header: r.Header.Clone(), body: body})
	s.mu.Unlock()
	if s.status == 0 {
		w.WriteHeader(http.StatusCreated)
		return
	}
	w.WriteHeader(s.status)
}

func (s *fakePushService) captured() []capturedPush {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]capturedPush(nil), s.requests...)
}

func silentLogger() *logrus.Logger {
	log := logrus.New()
	log.SetOutput(io.Discard)
	return log
}

// testPushService starts an httptest server that captures push requests
// and answers with the given status (201 when zero).
func testPushService(t *testing.T, status int) (*httptest.Server, *fakePushService) {
	t.Helper()
	svc := &fakePushService{status: status}
	srv := httptest.NewServer(svc)
	t.Cleanup(srv.Close)
	return srv, svc
}

// testSubscription returns a subscription whose endpoint points at the
// given server, with a real P-256 key pair and auth secret.
func testSubscription(t *testing.T, endpoint string) *entity.PushSubscription {
	t.Helper()
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate receiver key: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}
	return &entity.PushSubscription{
		ID:       "sub-1",
		UserID:   "user-b",
		Endpoint: endpoint,
		P256DH:   base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), x, y)),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}
}

func testNotifier(t *testing.T, store PushSubscriptionStore) *WebPushNotifier {
	t.Helper()
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate VAPID keys: %v", err)
	}
	return &WebPushNotifier{
		Enabled:         true,
		VAPIDPublicKey:  publicKey,
		VAPIDPrivateKey: privateKey,
		Subscriber:      "sos@example.com",
		TTLSeconds:      300,
		Subs:            store,
		Log:             silentLogger(),
	}
}

func TestWebPushNotifierBuildPayload(t *testing.T) {
	n := &WebPushNotifier{}
	raw, err := n.buildPayload("SOS Alert", "Emergency nearby.", map[string]string{
		"alert_id":  "a1",
		"latitude":  "-8.650000",
		"longitude": "115.220000",
	})
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	var payload struct {
		Title string            `json:"title"`
		Body  string            `json:"body"`
		Data  map[string]string `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v (%s)", err, raw)
	}
	if payload.Title != "SOS Alert" || payload.Body != "Emergency nearby." {
		t.Fatalf("title/body = %q/%q", payload.Title, payload.Body)
	}
	if payload.Data["url"] != "/" {
		t.Fatalf("data.url = %q, want /", payload.Data["url"])
	}
	if payload.Data["alert_id"] != "a1" {
		t.Fatalf("data.alert_id = %q, want a1", payload.Data["alert_id"])
	}
}

func TestWebPushNotifierSendsEncryptedNotification(t *testing.T) {
	srv, svc := testPushService(t, 0)
	store := newFakePushStore()
	sub := testSubscription(t, srv.URL+"/push")
	if err := store.Upsert(context.Background(), sub); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	n := testNotifier(t, store)
	n.HTTPClient = srv.Client()

	err := n.SendToUser(context.Background(), "user-b", "SOS Alert", "Emergency nearby.",
		map[string]string{"alert_id": "a1"})
	if err != nil {
		t.Fatalf("SendToUser: %v", err)
	}
	requests := svc.captured()
	if len(requests) != 1 {
		t.Fatalf("push service got %d requests, want 1", len(requests))
	}
	got := requests[0]
	if ttl := got.header.Get("TTL"); ttl != "300" {
		t.Errorf("TTL = %q, want 300", ttl)
	}
	if urgency := got.header.Get("Urgency"); urgency != string(webpush.UrgencyHigh) {
		t.Errorf("Urgency = %q, want %q", urgency, webpush.UrgencyHigh)
	}
	if enc := got.header.Get("Content-Encoding"); enc != "aes128gcm" {
		t.Errorf("Content-Encoding = %q, want aes128gcm", enc)
	}
	if auth := got.header.Get("Authorization"); !strings.HasPrefix(auth, "vapid t=") {
		t.Errorf("Authorization = %q, want vapid t=...", auth)
	}
	if len(got.body) == 0 {
		t.Error("push body is empty")
	}
	if strings.Contains(string(got.body), "Emergency nearby.") {
		t.Error("payload must be encrypted, found plaintext body")
	}
	if got.header.Get("Topic") != "" {
		t.Errorf("Topic must be unset (max 32 chars), got %q", got.header.Get("Topic"))
	}
}

func TestWebPushNotifierDeletesGoneSubscription(t *testing.T) {
	srv, _ := testPushService(t, http.StatusGone)
	store := newFakePushStore()
	sub := testSubscription(t, srv.URL+"/push")
	if err := store.Upsert(context.Background(), sub); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	n := testNotifier(t, store)
	n.HTTPClient = srv.Client()

	if err := n.SendToUser(context.Background(), "user-b", "SOS Alert", "Emergency nearby.", nil); err != nil {
		t.Fatalf("gone endpoint must not fail the dispatch: %v", err)
	}
	deleted := store.deletedEndpoints()
	if len(deleted) != 1 || deleted[0] != sub.Endpoint {
		t.Fatalf("deleted = %v, want [%s]", deleted, sub.Endpoint)
	}
}

func TestWebPushNotifierReportsServerError(t *testing.T) {
	srv, _ := testPushService(t, http.StatusInternalServerError)
	store := newFakePushStore()
	if err := store.Upsert(context.Background(), testSubscription(t, srv.URL+"/push")); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	n := testNotifier(t, store)
	n.HTTPClient = srv.Client()

	err := n.SendToUser(context.Background(), "user-b", "SOS Alert", "Emergency nearby.", nil)
	if err == nil {
		t.Fatal("500 from the push service must be reported as an error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %v, want it to mention the status code", err)
	}
	if len(store.deletedEndpoints()) != 0 {
		t.Fatal("a 500 must not delete the subscription")
	}
}

func TestWebPushNotifierNotConfigured(t *testing.T) {
	cases := map[string]*WebPushNotifier{
		"empty":                 {},
		"disabled":              {Enabled: false, VAPIDPublicKey: "k", VAPIDPrivateKey: "k", Subs: newFakePushStore()},
		"no keys":               {Enabled: true, Subs: newFakePushStore()},
		"no subscription store": {Enabled: true, VAPIDPublicKey: "k", VAPIDPrivateKey: "k", Subs: nil},
	}
	for name, n := range cases {
		if err := n.SendToUser(context.Background(), "user-b", "t", "b", nil); !errors.Is(err, ErrPushNotConfigured) {
			t.Errorf("%s: error = %v, want ErrPushNotConfigured", name, err)
		}
	}
}

func TestWebPushNotifierNoSubscriptionsIsNotAnError(t *testing.T) {
	store := newFakePushStore()
	n := testNotifier(t, store)
	if err := n.SendToUser(context.Background(), "user-b", "SOS Alert", "Emergency nearby.", nil); err != nil {
		t.Fatalf("a user without subscriptions must not fail dispatch: %v", err)
	}
}
