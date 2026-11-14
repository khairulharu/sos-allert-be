package config

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"
)

// An incident upload larger than Fiber's 4 MB default must reach the
// handler, otherwise every real audio/video capture fails with 413.
func TestNewFiberAllowsMediaUploadBody(t *testing.T) {
	app := NewFiber(viper.New())
	app.Post("/upload", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	body := bytes.Repeat([]byte("a"), 5<<20)
	resp, err := app.Test(httptest.NewRequest("POST", "/upload", bytes.NewReader(body)), -1)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 for a 5 MB body", resp.StatusCode)
	}
}

// A panicking handler (or an unsafe Locals assertion) must answer 500 with
// the documented error body instead of dropping the connection.
func TestNewFiberRecoversPanics(t *testing.T) {
	app := NewFiber(viper.New())
	app.Get("/panic", func(c *fiber.Ctx) error {
		panic(fiber.ErrInternalServerError)
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/panic", nil), -1)
	if err != nil {
		t.Fatalf("panic request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("status = %d body = %s, want 500", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "errors") {
		t.Fatalf("body = %s, want the {\"errors\": ...} shape", body)
	}
}

func TestNewFiberSecurityHeaders(t *testing.T) {
	app := NewFiber(viper.New())
	app.Get("/ping", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	resp, err := app.Test(httptest.NewRequest("GET", "/ping", nil), -1)
	if err != nil {
		t.Fatalf("ping request: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
}
