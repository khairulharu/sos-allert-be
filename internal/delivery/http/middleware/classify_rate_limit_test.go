package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang-clean-architecture/internal/model"

	"github.com/gofiber/fiber/v2"
)

type fakeClassifyLimiter struct {
	allowed bool
	err     error
	calls   int
}

func (f *fakeClassifyLimiter) AllowClassify(context.Context, string, int, time.Duration) (bool, error) {
	f.calls++
	return f.allowed, f.err
}

func newClassifyTestApp(limiter ClassifyRateLimiter) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return ctx.Status(code).JSON(fiber.Map{"errors": err.Error()})
		},
	})
	app.Use(func(ctx *fiber.Ctx) error {
		ctx.Locals("auth", &model.Auth{ID: "user-1"})
		return ctx.Next()
	})
	app.Post("/api/sos/classify", NewClassifyRateLimit(limiter, nil, 10, time.Hour),
		func(ctx *fiber.Ctx) error { return ctx.SendStatus(fiber.StatusOK) })
	return app
}

func TestClassifyRateLimitAllows(t *testing.T) {
	limiter := &fakeClassifyLimiter{allowed: true}
	resp, err := newClassifyTestApp(limiter).Test(httptest.NewRequest("POST", "/api/sos/classify", nil), -1)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || limiter.calls != 1 {
		t.Fatalf("status = %d calls = %d, want 200 and one limiter call", resp.StatusCode, limiter.calls)
	}
}

func TestClassifyRateLimitRejects(t *testing.T) {
	resp, err := newClassifyTestApp(&fakeClassifyLimiter{}).Test(httptest.NewRequest("POST", "/api/sos/classify", nil), -1)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "3600" {
		t.Fatalf("Retry-After = %q, want 3600", got)
	}
}

// A limiter outage must not let the paid call through.
func TestClassifyRateLimitFailsClosed(t *testing.T) {
	resp, err := newClassifyTestApp(&fakeClassifyLimiter{err: errors.New("redis down")}).
		Test(httptest.NewRequest("POST", "/api/sos/classify", nil), -1)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}
