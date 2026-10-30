package http

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestHealthStatus(t *testing.T) {
	app := fiber.New()
	ctrl := NewHealthController("rules")
	app.Get("/healthz", ctrl.Status)

	req := httptest.NewRequest("GET", "/healthz", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if payload["status"] != "ok" || payload["ai_mode"] != "rules" {
		t.Errorf("payload = %v", payload)
	}
	if _, ok := payload["time"]; !ok {
		t.Error("payload must include time")
	}

	// Empty mode defaults to rules.
	if NewHealthController("").AIMode != "rules" {
		t.Error("empty AI mode must default to rules")
	}
	if NewHealthController("llm").AIMode != "llm" {
		t.Error("llm AI mode must be preserved")
	}
}
