package usecase

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	d := NewAlertDispatcher(noopLogger(), &FCMNotifier{}, &APNsNotifier{}, &TwilioNotifier{}, nil, nil, nil)
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
	d := NewAlertDispatcher(noopLogger(), fcm, nil, nil, nil, contacts, nil)
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
