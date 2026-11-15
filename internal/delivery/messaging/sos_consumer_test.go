package messaging

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/usecase"

	"github.com/IBM/sarama"
	"github.com/sirupsen/logrus"
)

type captureNotifier struct {
	recipients []string
	alert      model.Alert
	calls      int
	pushCalls  int
}

func (c *captureNotifier) Notify(ctx context.Context, recipientIDs []string, alert model.Alert) error {
	c.calls++
	c.recipients = recipientIDs
	c.alert = alert
	return nil
}

func (c *captureNotifier) NotifyRecipients(ctx context.Context, recipientIDs []string, alert model.Alert) error {
	c.pushCalls++
	c.recipients = recipientIDs
	c.alert = alert
	return nil
}

func TestSOSConsumerCarriesTriageToNotifier(t *testing.T) {
	event := model.SOSEvent{
		EventType:     "sos.dispatched",
		AlertID:       "a1",
		SenderID:      "sender",
		EmergencyType: "MEDICAL",
		Status:        "TRIGGERED",
		Recipients:    []string{"user-b"},
		TriggeredAt:   1700000000000,
		Severity:      0.8,
		Summary:       "PRIORITAS TINGGI: Kemungkinan darurat medis",
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	notifier := &captureNotifier{}
	consumer := NewSOSConsumer(logrus.New(), notifier)
	if err := consumer.Consume(&sarama.ConsumerMessage{Value: payload}); err != nil {
		t.Fatalf("Consume error = %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("notifier calls = %d, want 1", notifier.calls)
	}
	if notifier.pushCalls != 0 {
		t.Fatalf("push-only calls = %d, want 0 for a normal dispatch", notifier.pushCalls)
	}
	if notifier.alert.Severity != event.Severity {
		t.Fatalf("alert severity = %v, want %v", notifier.alert.Severity, event.Severity)
	}
	if notifier.alert.Summary != event.Summary {
		t.Fatalf("alert summary = %q, want %q", notifier.alert.Summary, event.Summary)
	}
	if len(notifier.recipients) != 1 || notifier.recipients[0] != "user-b" {
		t.Fatalf("recipients = %v, want [user-b]", notifier.recipients)
	}
}

// An escalation event reaches only the responders added by the wider
// radius, so the consumer must use the push-only surface.
func TestSOSConsumerEscalationUsesPushOnlyNotifier(t *testing.T) {
	event := model.SOSEvent{
		EventType:     "sos.dispatched",
		AlertID:       "a1",
		SenderID:      "sender",
		EmergencyType: "MEDICAL",
		Status:        "TRIGGERED",
		Recipients:    []string{"user-c"},
		TriggeredAt:   1700000000000,
		PushOnly:      true,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	notifier := &captureNotifier{}
	consumer := NewSOSConsumer(logrus.New(), notifier)
	if err := consumer.Consume(&sarama.ConsumerMessage{Value: payload}); err != nil {
		t.Fatalf("Consume error = %v", err)
	}
	if notifier.calls != 0 {
		t.Fatalf("full fan-out calls = %d, want 0 for a push-only event", notifier.calls)
	}
	if notifier.pushCalls != 1 {
		t.Fatalf("push-only calls = %d, want 1", notifier.pushCalls)
	}
	if len(notifier.recipients) != 1 || notifier.recipients[0] != "user-c" {
		t.Fatalf("recipients = %v, want [user-c]", notifier.recipients)
	}
}

// fakeTokenReader stands in for repository.UserReaderRepository.
type fakeTokenReader struct {
	users   map[string]*entity.User
	lookups []string
	mu      sync.Mutex
}

func (f *fakeTokenReader) GetByID(ctx context.Context, id string) (*entity.User, error) {
	f.mu.Lock()
	f.lookups = append(f.lookups, id)
	f.mu.Unlock()
	user, ok := f.users[id]
	if !ok {
		return nil, context.Canceled
	}
	return user, nil
}

func (f *fakeTokenReader) ListByIDs(ctx context.Context, ids []string) (map[string]*entity.User, error) {
	out := make(map[string]*entity.User, len(ids))
	for _, id := range ids {
		if user, ok := f.users[id]; ok {
			out[id] = user
		}
	}
	return out, nil
}

func (f *fakeTokenReader) lookupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.lookups)
}

// fakeContactResolver stands in for repository.DispatchContactRepository
// and records how often the SMS fallback ran.
type fakeContactResolver struct {
	contacts []model.EmergencyContactResponse
	senders  []string
	mu       sync.Mutex
}

func (f *fakeContactResolver) DispatchContacts(ctx context.Context, userID string) ([]model.EmergencyContactResponse, error) {
	f.mu.Lock()
	f.senders = append(f.senders, userID)
	f.mu.Unlock()
	return f.contacts, nil
}

func (f *fakeContactResolver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.senders)
}

func testAPNsPrivateKey(t *testing.T) []byte {
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

// gadgetServer accepts APNs and Twilio deliveries and records the paths.
type gadgetServer struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newGadgetServer(t *testing.T) *gadgetServer {
	t.Helper()
	g := &gadgetServer{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.paths = append(g.paths, r.URL.Path)
		g.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"sid":"SM1"}`))
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *gadgetServer) captured() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.paths...)
}

// newWorkerDispatcher mirrors cmd/worker: DB-backed token/contact
// resolvers plus the configured push/SMS channels.
func newWorkerDispatcher(t *testing.T, gateway *gadgetServer, tokens usecase.TokenReader, contacts usecase.ContactResolver) *usecase.AlertDispatcher {
	t.Helper()
	apns := &usecase.APNsNotifier{
		Enabled:       true,
		KeyID:         "K1",
		TeamID:        "T1",
		BundleID:      "id.allert.app",
		PrivateKeyPEM: testAPNsPrivateKey(t),
		BaseURL:       gateway.URL,
		HTTPClient:    gateway.Client(),
	}
	twilio := &usecase.TwilioNotifier{
		Enabled:    true,
		AccountSID: "AC1",
		AuthToken:  "secret",
		FromNumber: "+1555",
		BaseURL:    gateway.URL,
		HTTPClient: gateway.Client(),
	}
	return usecase.NewAlertDispatcher(logrus.New(), nil, apns, twilio, nil, tokens, contacts, nil)
}

func marshalEvent(t *testing.T, event model.SOSEvent) []byte {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return payload
}

// The worker must reach APNs (device token from the DB) and the sender's
// emergency contacts (SMS fallback), matching the direct web path.
func TestSOSConsumerWorkerDispatchReachesAPNsAndContacts(t *testing.T) {
	gateway := newGadgetServer(t)
	apnsToken := "apns-device-token"
	tokens := &fakeTokenReader{users: map[string]*entity.User{
		"user-b": {ID: "user-b", APNsToken: &apnsToken},
	}}
	contacts := &fakeContactResolver{contacts: []model.EmergencyContactResponse{
		{ID: "c1", ContactName: "Ibu", PhoneNumber: "+62812"},
	}}
	consumer := NewSOSConsumer(logrus.New(), newWorkerDispatcher(t, gateway, tokens, contacts))

	event := model.SOSEvent{
		EventType:     "sos.dispatched",
		AlertID:       "a1",
		SenderID:      "sender",
		EmergencyType: "MEDICAL",
		Status:        "TRIGGERED",
		Recipients:    []string{"user-b"},
		TriggeredAt:   1700000000000,
		Latitude:      -8.65,
		Longitude:     115.22,
	}
	if err := consumer.Consume(&sarama.ConsumerMessage{Value: marshalEvent(t, event)}); err != nil {
		t.Fatalf("Consume error = %v", err)
	}

	if got := tokens.lookupCount(); got == 0 {
		t.Fatal("no push-token lookup: the worker cannot reach APNs without it")
	}
	if got := contacts.callCount(); got != 1 {
		t.Fatalf("contact resolver calls = %d, want 1 for a normal dispatch", got)
	}
	if len(contacts.senders) != 1 || contacts.senders[0] != "sender" {
		t.Fatalf("contact lookups = %v, want [sender]", contacts.senders)
	}

	var apnsHits, smsHits int
	for _, path := range gateway.captured() {
		switch {
		case strings.Contains(path, "/3/device/"+apnsToken):
			apnsHits++
		case strings.Contains(path, "/Messages.json"):
			smsHits++
		}
	}
	if apnsHits != 1 {
		t.Fatalf("APNs deliveries = %d, want 1 (paths %v)", apnsHits, gateway.captured())
	}
	if smsHits != 1 {
		t.Fatalf("SMS deliveries = %d, want 1 (paths %v)", smsHits, gateway.captured())
	}
}

// The escalated fan-out is push-only: responders added by the wider
// radius get notified, but the sender's emergency contacts are not
// texted a second time.
func TestSOSConsumerEscalationSkipsContactSMS(t *testing.T) {
	gateway := newGadgetServer(t)
	apnsToken := "apns-device-token"
	tokens := &fakeTokenReader{users: map[string]*entity.User{
		"user-c": {ID: "user-c", APNsToken: &apnsToken},
	}}
	contacts := &fakeContactResolver{contacts: []model.EmergencyContactResponse{
		{ID: "c1", ContactName: "Ibu", PhoneNumber: "+62812"},
	}}
	consumer := NewSOSConsumer(logrus.New(), newWorkerDispatcher(t, gateway, tokens, contacts))

	event := model.SOSEvent{
		EventType:     "sos.dispatched",
		AlertID:       "a1",
		SenderID:      "sender",
		EmergencyType: "MEDICAL",
		Status:        "TRIGGERED",
		Recipients:    []string{"user-c"},
		TriggeredAt:   1700000000000,
		Severity:      0.9,
		PushOnly:      true,
	}
	if err := consumer.Consume(&sarama.ConsumerMessage{Value: marshalEvent(t, event)}); err != nil {
		t.Fatalf("Consume error = %v", err)
	}

	if got := contacts.callCount(); got != 0 {
		t.Fatalf("contact resolver calls = %d, want 0 for a push-only escalation", got)
	}
	var apnsHits int
	for _, path := range gateway.captured() {
		if strings.Contains(path, "/3/device/"+apnsToken) {
			apnsHits++
		}
		if strings.Contains(path, "/Messages.json") {
			t.Errorf("unexpected SMS to the sender's emergency contacts: %s", path)
		}
	}
	if apnsHits != 1 {
		t.Fatalf("APNs deliveries = %d, want 1 (paths %v)", apnsHits, gateway.captured())
	}
}

// hungNotifier blocks until the consumer's per-message deadline, standing in
// for a push/SMS provider that accepted the connection and stopped answering.
type hungNotifier struct {
	deadlineSeen bool
}

func (h *hungNotifier) Notify(ctx context.Context, recipientIDs []string, alert model.Alert) error {
	<-ctx.Done()
	h.deadlineSeen = errors.Is(ctx.Err(), context.DeadlineExceeded)
	return ctx.Err()
}

// One message must not block the partition forever: Consume runs the delivery
// under its own deadline and reports the failure so the consumer group can
// retry or dead-letter it (#42).
func TestSOSConsumerBoundsOneMessage(t *testing.T) {
	notifier := &hungNotifier{}
	consumer := NewSOSConsumer(logrus.New(), notifier)
	consumer.Timeout = 20 * time.Millisecond

	event := model.SOSEvent{
		EventType:   "sos.dispatched",
		AlertID:     "a1",
		SenderID:    "sender",
		Status:      "TRIGGERED",
		Recipients:  []string{"user-b"},
		TriggeredAt: 1700000000000,
	}
	start := time.Now()
	err := consumer.Consume(&sarama.ConsumerMessage{Value: marshalEvent(t, event)})
	if err == nil {
		t.Fatal("Consume = nil, want the deadline to surface as a delivery error")
	}
	if !notifier.deadlineSeen {
		t.Fatal("notifier context was not cancelled with a deadline")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Consume took %v, want it bounded by the per-message timeout", elapsed)
	}
}

// A zero Timeout keeps the documented default bound.
func TestSOSConsumerDefaultTimeout(t *testing.T) {
	consumer := &SOSConsumer{}
	if got := consumer.timeout(); got != SosConsumeTimeout {
		t.Fatalf("timeout = %v, want %v", got, SosConsumeTimeout)
	}
	if SosConsumeTimeout <= 0 || SosConsumeTimeout > time.Minute {
		t.Fatalf("SosConsumeTimeout = %v, want a sane per-message bound", SosConsumeTimeout)
	}
}
