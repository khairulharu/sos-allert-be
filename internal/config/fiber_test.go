package config

import (
	"bytes"
	"encoding/json"
	"errors"
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

// A 5xx must never echo internal detail (file paths, SQL, provider
// bodies); 4xx messages stay as-is because the client needs them.
func TestErrorHandlerMasksInternalErrors(t *testing.T) {
	app := NewFiber(viper.New())
	app.Get("/boom", func(c *fiber.Ctx) error {
		return errors.New("dial tcp 10.0.0.5:3306: /srv/app/config.json: no such host")
	})
	app.Get("/fiber-boom", func(c *fiber.Ctx) error {
		return fiber.ErrInternalServerError
	})
	app.Get("/bad", func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "phone number must be in E.164 format")
	})

	cases := []struct {
		path    string
		status  int
		message string
	}{
		{"/boom", fiber.StatusInternalServerError, genericInternalMessage},
		{"/fiber-boom", fiber.StatusInternalServerError, genericInternalMessage},
		{"/bad", fiber.StatusUnprocessableEntity, "phone number must be in E.164 format"},
	}
	for _, tc := range cases {
		resp, err := app.Test(httptest.NewRequest("GET", tc.path, nil), -1)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%s status = %d body = %s, want %d", tc.path, resp.StatusCode, body, tc.status)
		}
		var got struct {
			Errors string `json:"errors"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("%s body is not JSON: %v (%s)", tc.path, err, body)
		}
		if got.Errors != tc.message {
			t.Fatalf("%s errors = %q, want %q", tc.path, got.Errors, tc.message)
		}
		for _, leaked := range []string{"config.json", "3306", "10.0.0.5"} {
			if strings.Contains(string(body), leaked) {
				t.Fatalf("%s body leaks %q: %s", tc.path, leaked, body)
			}
		}
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
