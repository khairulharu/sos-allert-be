package http_test

// HTTP tests for the browser Web Push subscription surface: subscribe,
// list per user, unsubscribe, and the VAPID public-key endpoint. All
// stores are in-memory, so no MySQL is needed.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	deliveryhttp "golang-clean-architecture/internal/delivery/http"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/usecase"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

// memPushStore is an in-memory PushSubscriptionStore keyed by endpoint.
type memPushStore struct {
	mu   sync.Mutex
	rows []*entity.PushSubscription
}

func (s *memPushStore) Upsert(ctx context.Context, sub *entity.PushSubscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, row := range s.rows {
		if row.Endpoint == sub.Endpoint {
			s.rows[i] = sub
			return nil
		}
	}
	s.rows = append(s.rows, sub)
	return nil
}

func (s *memPushStore) ListByUserID(ctx context.Context, userID string) ([]*entity.PushSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*entity.PushSubscription
	for _, row := range s.rows {
		if row.UserID == userID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *memPushStore) DeleteByUserAndEndpoint(ctx context.Context, userID, endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, row := range s.rows {
		if row.UserID == userID && row.Endpoint == endpoint {
			s.rows = append(s.rows[:i], s.rows[i+1:]...)
			return nil
		}
	}
	return nil
}

func (s *memPushStore) DeleteByEndpoint(ctx context.Context, endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, row := range s.rows {
		if row.Endpoint == endpoint {
			s.rows = append(s.rows[:i], s.rows[i+1:]...)
			return nil
		}
	}
	return nil
}

func TestPushAPIEndToEnd(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	store := &memPushStore{}
	uc := usecase.NewPushUseCase(nil, log, validator.New(), store, "BO_TEST_PUBLIC_KEY", true, nil)

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("auth", &model.Auth{ID: strings.Clone(c.Get("X-Test-User"))})
		return c.Next()
	})
	ctl := deliveryhttp.NewPushController(uc, log)
	app.Get("/api/push/vapid-public-key", ctl.VAPIDPublicKey)
	app.Get("/api/push/subscriptions", ctl.List)
	app.Post("/api/push/subscriptions", ctl.Subscribe)
	app.Delete("/api/push/subscriptions", ctl.Unsubscribe)

	do := func(method, path, user, body string) (int, []byte) {
		t.Helper()
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, path, nil)
		} else {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("X-Test-User", user)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	const endpointA = "https://fcm.googleapis.com/fcm/send/sub-a"
	subscribeBody := `{"endpoint":"` + endpointA + `","p256dh":"BNcR","auth":"secret","user_agent":"test-agent"}`

	// Unconfigured channel: 503 until the VAPID keys are present.
	disabled := usecase.NewPushUseCase(nil, log, validator.New(), store, "BO_TEST_PUBLIC_KEY", false, nil)
	disabledCtl := deliveryhttp.NewPushController(disabled, log)
	disabledApp := fiber.New()
	disabledApp.Get("/api/push/vapid-public-key", disabledCtl.VAPIDPublicKey)
	resp, err := disabledApp.Test(httptest.NewRequest("GET", "/api/push/vapid-public-key", nil), -1)
	if err != nil {
		t.Fatalf("disabled vapid key: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled vapid key status = %d, want 503", resp.StatusCode)
	}
	resp.Body.Close()

	// The configured key is served verbatim.
	status, body := do("GET", "/api/push/vapid-public-key", "user-a", "")
	if status != http.StatusOK {
		t.Fatalf("vapid key status = %d body = %s", status, body)
	}
	var keyResp struct {
		Data struct {
			PublicKey string `json:"public_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &keyResp); err != nil {
		t.Fatalf("vapid key is not JSON: %v (%s)", err, body)
	}
	if keyResp.Data.PublicKey != "BO_TEST_PUBLIC_KEY" {
		t.Fatalf("public_key = %q", keyResp.Data.PublicKey)
	}

	// Subscribe as user-a, then list: only user-a sees the row.
	status, body = do("POST", "/api/push/subscriptions", "user-a", subscribeBody)
	if status != http.StatusOK {
		t.Fatalf("subscribe status = %d body = %s", status, body)
	}
	status, body = do("GET", "/api/push/subscriptions", "user-a", "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d body = %s", status, body)
	}
	var listResp struct {
		Data []model.PushSubscriptionResponse `json:"data"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("list is not JSON: %v (%s)", err, body)
	}
	if len(listResp.Data) != 1 || listResp.Data[0].Endpoint != endpointA {
		t.Fatalf("user-a subscriptions = %+v, want the endpoint just registered", listResp.Data)
	}
	status, body = do("GET", "/api/push/subscriptions", "user-b", "")
	if status != http.StatusOK {
		t.Fatalf("list as user-b status = %d body = %s", status, body)
	}
	if !strings.Contains(string(body), `"data":[]`) {
		t.Fatalf("user-b must not see user-a's subscription, got %s", body)
	}

	// Re-subscribing the same endpoint keeps a single row (upsert).
	status, body = do("POST", "/api/push/subscriptions", "user-a", subscribeBody)
	if status != http.StatusOK {
		t.Fatalf("re-subscribe status = %d body = %s", status, body)
	}
	status, body = do("GET", "/api/push/subscriptions", "user-a", "")
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("list is not JSON: %v (%s)", err, body)
	}
	if len(listResp.Data) != 1 {
		t.Fatalf("re-subscribe must upsert, got %d rows", len(listResp.Data))
	}

	// Another user re-registering the same browser re-points the row.
	status, body = do("POST", "/api/push/subscriptions", "user-b", subscribeBody)
	if status != http.StatusOK {
		t.Fatalf("re-point status = %d body = %s", status, body)
	}
	status, body = do("GET", "/api/push/subscriptions", "user-a", "")
	if !strings.Contains(string(body), `"data":[]`) {
		t.Fatalf("user-a must lose the row after user-b re-subscribes, got %s", body)
	}

	// Missing endpoint is rejected before touching the store.
	status, body = do("POST", "/api/push/subscriptions", "user-b", `{"p256dh":"BNcR","auth":"secret"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("subscribe without endpoint status = %d body = %s, want 400", status, body)
	}

	// An endpoint outside the push-service allowlist is refused: storing it
	// would make every dispatch POST to an attacker-chosen host (SSRF).
	for _, evil := range []string{"http://127.0.0.1:3000/x", "http://169.254.169.254/latest/meta-data/"} {
		status, body = do("POST", "/api/push/subscriptions", "user-b",
			`{"endpoint":"`+evil+`","p256dh":"BNcR","auth":"secret"}`)
		if status != http.StatusBadRequest {
			t.Fatalf("subscribe %s status = %d body = %s, want 400", evil, status, body)
		}
	}

	// Unsubscribe removes the row.
	status, body = do("DELETE", "/api/push/subscriptions", "user-b", `{"endpoint":"`+endpointA+`"}`)
	if status != http.StatusOK {
		t.Fatalf("unsubscribe status = %d body = %s", status, body)
	}
	status, body = do("GET", "/api/push/subscriptions", "user-b", "")
	if !strings.Contains(string(body), `"data":[]`) {
		t.Fatalf("subscription must be gone after unsubscribe, got %s", body)
	}
}
