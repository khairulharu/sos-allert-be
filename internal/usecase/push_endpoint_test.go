package usecase

import (
	"context"
	"testing"

	"golang-clean-architecture/internal/model"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
)

func TestIsAllowedWebPushEndpoint(t *testing.T) {
	allowed := []string{"fcm.googleapis.com", ".notify.windows.com"}
	cases := map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":            true,
		"https://FCM.googleapis.com/fcm/send/abc":            true,
		"https://wns2-par02p.notify.windows.com/w/?token=ab": true,
		"https://evil.com/x":                                 false,
		// Host look-alikes and IP literals an attacker would try.
		"https://fcm.googleapis.com.evil.com/x":      false,
		"https://fcm.googleapis.com:443@127.0.0.1/x": false,
		"http://127.0.0.1:3000/x":                    false,
		"http://169.254.169.254/latest/meta-data/":   false,
		"http://[::1]/":                              false,
		"not a url":                                  false,
		"":                                           false,
	}
	for endpoint, want := range cases {
		if got := IsAllowedWebPushEndpoint(endpoint, allowed); got != want {
			t.Errorf("IsAllowedWebPushEndpoint(%q) = %v, want %v", endpoint, got, want)
		}
	}
}

func TestPushUseCaseRejectsForeignEndpoint(t *testing.T) {
	store := newFakePushStore()
	uc := NewPushUseCase(nil, silentLogger(), validator.New(), store, "BO_KEY", true, nil)

	_, err := uc.Subscribe(context.Background(), &model.PushSubscribeRequest{
		UserID: "user-a", Endpoint: "http://127.0.0.1:3000/ssrf", P256DH: "BNcR", Auth: "secret",
	})
	if err != fiber.ErrBadRequest {
		t.Fatalf("Subscribe error = %v, want fiber.ErrBadRequest", err)
	}

	if _, err := uc.Subscribe(context.Background(), &model.PushSubscribeRequest{
		UserID: "user-a", Endpoint: "https://fcm.googleapis.com/fcm/send/sub-a", P256DH: "BNcR", Auth: "secret",
	}); err != nil {
		t.Fatalf("allowlisted Subscribe error = %v, want nil", err)
	}
}

// A row stored before the allowlist existed must never become a POST target,
// and the notifier removes it.
func TestWebPushNotifierDropsForeignEndpoint(t *testing.T) {
	srv, svc := testPushService(t, 0)
	store := newFakePushStore()
	sub := testSubscription(t, srv.URL+"/ssrf")
	if err := store.Upsert(context.Background(), sub); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	// Default allowlist: the local test endpoint is not on it.
	n := testNotifier(t, store)
	n.AllowedHosts = nil
	if err := n.SendToUser(context.Background(), "user-b", "SOS", "help", nil); err != nil {
		t.Fatalf("SendToUser error = %v, want nil (nothing to deliver)", err)
	}
	if got := len(svc.captured()); got != 0 {
		t.Fatalf("push service got %d requests, want 0", got)
	}
	found := false
	for _, endpoint := range store.deletedEndpoints() {
		if endpoint == sub.Endpoint {
			found = true
		}
	}
	if !found {
		t.Fatalf("deleted endpoints = %v, want %q", store.deletedEndpoints(), sub.Endpoint)
	}
}
