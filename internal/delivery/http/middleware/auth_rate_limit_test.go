package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

type fakeAuthLimiter struct {
	allowed    bool
	retryAfter time.Duration
	err        error

	calls      int
	gotIP      string
	gotLimit   int
	gotWindow  time.Duration
	gotLockout time.Duration
}

func (f *fakeAuthLimiter) AllowAuth(ctx context.Context, ip string, limit int, window, lockout time.Duration) (bool, time.Duration, error) {
	f.calls++
	f.gotIP = ip
	f.gotLimit = limit
	f.gotWindow = window
	f.gotLockout = lockout
	return f.allowed, f.retryAfter, f.err
}

func newAuthRateTestApp(limiter AuthRateLimiter) *fiber.App {
	log := logrus.New()
	log.SetOutput(io.Discard)

	app := fiber.New(fiber.Config{
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return ctx.Status(code).JSON(fiber.Map{"errors": err.Error()})
		},
	})
	app.Post("/login",
		NewAuthRateLimit(limiter, log, 5, time.Minute, 15*time.Minute),
		func(ctx *fiber.Ctx) error { return ctx.SendStatus(fiber.StatusOK) },
	)
	return app
}

func authRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	return req
}

func TestAuthRateLimitAllowsWithinLimit(t *testing.T) {
	limiter := &fakeAuthLimiter{allowed: true}
	app := newAuthRateTestApp(limiter)

	resp, err := app.Test(authRequest())
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if limiter.calls != 1 || limiter.gotIP == "" {
		t.Fatalf("limiter calls/ip = %d/%q", limiter.calls, limiter.gotIP)
	}
	if limiter.gotLimit != 5 || limiter.gotWindow != time.Minute || limiter.gotLockout != 15*time.Minute {
		t.Fatalf("limiter params = %d/%v/%v", limiter.gotLimit, limiter.gotWindow, limiter.gotLockout)
	}
}

func TestAuthRateLimitBlocksWithRetryAfter(t *testing.T) {
	limiter := &fakeAuthLimiter{allowed: false, retryAfter: 15 * time.Minute}
	app := newAuthRateTestApp(limiter)

	resp, err := app.Test(authRequest())
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "900" {
		t.Fatalf("Retry-After = %q, want 900", got)
	}
}

func TestAuthRateLimitFailsOpen(t *testing.T) {
	limiter := &fakeAuthLimiter{err: context.DeadlineExceeded}
	app := newAuthRateTestApp(limiter)

	resp, err := app.Test(authRequest())
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fail-open)", resp.StatusCode)
	}
}

func TestAuthRateLimitNilLimiterPassesThrough(t *testing.T) {
	app := newAuthRateTestApp(nil)

	resp, err := app.Test(authRequest())
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
