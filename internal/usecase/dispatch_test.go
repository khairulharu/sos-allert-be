package usecase

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
	"testing"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
)

func TestFCMNotifierDisabled(t *testing.T) {
	n := &FCMNotifier{}
	if err := n.SendToTopic(context.Background(), "user_1", "t", "b", nil); !errors.Is(err, ErrPushNotConfigured) {
		t.Fatalf("error = %v, want ErrPushNotConfigured", err)
	}
}

func TestFCMNotifierSends(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		if !strings.HasSuffix(r.URL.Path, "/v1/projects/proj-1/messages:send") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"projects/proj-1/messages/1"}`))
	}))
	defer srv.Close()

	n := &FCMNotifier{Enabled: true, ProjectID: "proj-1", AccessToken: "tok", BaseURL: srv.URL, HTTPClient: srv.Client()}
	if err := n.SendToTopic(context.Background(), "user_1", "SOS Alert", "help", map[string]string{"alert_id": "a1"}); err != nil {
		t.Fatalf("send error = %v", err)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"topic":"user_1"`) || !strings.Contains(gotBody, "siren") {
		t.Fatalf("body = %s", gotBody)
	}
}

func TestAlertDispatcherPushCarriesTriage(t *testing.T) {
	var got fcmMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode FCM body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fcm := &FCMNotifier{Enabled: true, ProjectID: "p", AccessToken: "tok", BaseURL: srv.URL, HTTPClient: srv.Client()}
	deviceToken := "device-token"
	tokens := &fakeUserReader{users: map[string]*entity.User{
		"user-b": {ID: "user-b", FCMToken: &deviceToken},
	}}
	d := NewAlertDispatcher(noopLogger(), fcm, nil, nil, nil, tokens, nil, nil)
	alert := model.Alert{
		ID:            "a1",
		SenderId:      "sender",
		EmergencyType: entity.EmergencyTypeMedical,
		Severity:      0.8,
		Summary:       "PRIORITAS TINGGI: Kemungkinan darurat medis",
	}
	if err := d.Notify(context.Background(), []string{"user-b"}, alert); err != nil {
		t.Fatalf("Notify error = %v", err)
	}
	if got.Message.Data["severity"] != "0.80" {
		t.Fatalf("severity data = %q, want 0.80", got.Message.Data["severity"])
	}
	if got.Message.Data["summary"] != alert.Summary {
		t.Fatalf("summary data = %q, want %q", got.Message.Data["summary"], alert.Summary)
	}
	if body := got.Message.Notification.Body; !strings.Contains(body, alert.Summary) {
		t.Fatalf("push body = %q, want it to contain the AI summary", body)
	}
}

func TestTwilioNotifierSends(t *testing.T) {
	var gotForm string
	var gotUser, gotPass string
	var ok bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, ok = r.BasicAuth()
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		gotForm = string(body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM1"}`))
	}))
	defer srv.Close()

	n := &TwilioNotifier{Enabled: true, AccountSID: "AC1", AuthToken: "secret", FromNumber: "+1555", BaseURL: srv.URL, HTTPClient: srv.Client()}
	if err := n.SendSMS(context.Background(), "+62812", "SOS"); err != nil {
		t.Fatalf("send error = %v", err)
	}
	if !ok || gotUser != "AC1" || gotPass != "secret" {
		t.Fatalf("basic auth = %s/%s ok=%v", gotUser, gotPass, ok)
	}
	if !strings.Contains(gotForm, "To=%2B62812") || !strings.Contains(gotForm, "From=%2B1555") {
		t.Fatalf("form = %s", gotForm)
	}
}

func TestAPNsJWT(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	n := &APNsNotifier{KeyID: "K1", TeamID: "T1", PrivateKeyPEM: pemBytes}
	token, err := n.BuildJWT()
	if err != nil {
		t.Fatalf("BuildJWT error = %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		t.Fatalf("token = %q", token)
	}
	// Disabled APNs reports not-configured instead of failing.
	disabled := &APNsNotifier{}
	if err := disabled.Send(context.Background(), "tok", "t", "b", "a1"); !errors.Is(err, ErrPushNotConfigured) {
		t.Fatalf("error = %v, want ErrPushNotConfigured", err)
	}
}

func TestAlertDispatcherLogFallback(t *testing.T) {
	d := NewAlertDispatcher(noopLogger(), &FCMNotifier{}, &APNsNotifier{}, &TwilioNotifier{}, nil, nil, nil, nil)
	alert := model.Alert{ID: "a1", SenderId: "sender", EmergencyType: "DANGER"}
	if err := d.Notify(context.Background(), []string{"user-b"}, alert); err != nil {
		t.Fatalf("unconfigured dispatcher must not fail the SOS, got %v", err)
	}
}

func TestAlertDispatcherSMSFallbackOnPushFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	fcm := &FCMNotifier{Enabled: true, ProjectID: "p", AccessToken: "tok", BaseURL: srv.URL, HTTPClient: srv.Client()}
	contacts := &fakeDispatchContacts{contacts: []model.EmergencyContactResponse{{ContactName: "Ibu", PhoneNumber: "+62812"}}}
	d := NewAlertDispatcher(noopLogger(), fcm, nil, nil, nil, nil, contacts, nil)
	alert := model.Alert{ID: "a1", SenderId: "sender", EmergencyType: "DANGER"}
	// Twilio is nil, so SMS degrades to logging; push failure alone
	// must not fail the SOS (the use case only logs Notify errors).
	if err := d.Notify(context.Background(), []string{"user-b"}, alert); err != nil {
		t.Fatalf("Notify error = %v", err)
	}
}

type fakeDispatchContacts struct {
	contacts []model.EmergencyContactResponse
}

func (f *fakeDispatchContacts) DispatchContacts(ctx context.Context, userID string) ([]model.EmergencyContactResponse, error) {
	return f.contacts, nil
}

func TestAlertDispatcherSendsWebPush(t *testing.T) {
	srv, svc := testPushService(t, 0)
	store := newFakePushStore()
	for _, userID := range []string{"user-b", "user-c"} {
		sub := testSubscription(t, srv.URL+"/push/"+userID)
		sub.ID = "sub-" + userID
		sub.UserID = userID
		if err := store.Upsert(context.Background(), sub); err != nil {
			t.Fatalf("seed store: %v", err)
		}
	}
	web := testNotifier(t, store)
	web.HTTPClient = srv.Client()

	d := NewAlertDispatcher(noopLogger(), nil, nil, nil, web, nil, nil, nil)
	alert := model.Alert{
		ID:            "a1",
		SenderId:      "sender",
		EmergencyType: "DANGER",
		Summary:       "Person collapsed",
		Location:      model.LocationResponse{Latitude: -8.65, Longitude: 115.22},
	}
	if err := d.Notify(context.Background(), []string{"user-b", "user-c"}, alert); err != nil {
		t.Fatalf("Notify error = %v", err)
	}
	requests := svc.captured()
	if len(requests) != 2 {
		t.Fatalf("push service got %d requests, want one per recipient (2)", len(requests))
	}
	for _, got := range requests {
		if ttl := got.header.Get("TTL"); ttl != "300" {
			t.Errorf("TTL = %q, want 300", ttl)
		}
		if len(got.body) == 0 {
			t.Error("push body is empty")
		}
	}
}
