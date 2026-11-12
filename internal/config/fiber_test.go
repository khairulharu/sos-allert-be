package config

import (
	"bytes"
	"net/http/httptest"
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
