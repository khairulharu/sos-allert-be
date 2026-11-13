package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestHandlerRequiresToken(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		code := fiber.StatusInternalServerError
		if e, ok := err.(*fiber.Error); ok {
			code = e.Code
		}
		return c.Status(code).SendString(err.Error())
	}})
	app.Get("/metrics", New().Handler("s3cret"))

	cases := []struct {
		auth string
		want int
	}{
		{"", 401},
		{"wrong", 401},
		{"s3cret", 200},
		{"Bearer s3cret", 200},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		res, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if res.StatusCode != tc.want {
			t.Fatalf("auth %q: got %d, want %d", tc.auth, res.StatusCode, tc.want)
		}
	}
}

func TestRender(t *testing.T) {
	m := New()
	m.ObserveDispatchDuration(120)
	m.ObserveDispatchDuration(300)
	m.IncPushFailures()
	m.SetActiveSOS(2)
	out := m.Render()
	for _, want := range []string{
		`sos_dispatch_latency_ms_count 2`,
		`sos_dispatch_latency_ms_bucket{le="150"} 1`,
		`sos_dispatch_latency_ms_bucket{le="+Inf"} 2`,
		`push_delivery_failures_total 1`,
		`active_sos_count 2`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}
