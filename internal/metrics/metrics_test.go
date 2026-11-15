package metrics

import (
	"fmt"
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
	m.IncFanoutFailures()
	m.SetActiveSOS(2)
	out := m.Render()
	for _, want := range []string{
		`sos_dispatch_latency_ms_count 2`,
		`sos_dispatch_latency_ms_bucket{le="150"} 1`,
		`sos_dispatch_latency_ms_bucket{le="+Inf"} 2`,
		`push_delivery_failures_total 1`,
		`sos_fanout_failures_total 1`,
		`active_sos_count 2`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

// TestRenderHistogramIsCumulativeOnce guards against the double-cumulative
// bug: ObserveDispatchDuration used to increment every bucket the
// observation fit into, and Render accumulated them again.
func TestRenderHistogramIsCumulativeOnce(t *testing.T) {
	m := New()
	m.ObserveDispatchDuration(120)
	m.ObserveDispatchDuration(300)
	out := m.Render()

	want := map[string]string{
		"50":   "0",
		"100":  "0",
		"150":  "1",
		"200":  "1",
		"500":  "2",
		"1000": "2",
		"2000": "2",
		"+Inf": "2",
	}
	got := map[string]uint64{}
	for _, bound := range []string{"50", "100", "150", "200", "500", "1000", "2000", "+Inf"} {
		line := `sos_dispatch_latency_ms_bucket{le="` + bound + `"} `
		idx := strings.Index(out, line)
		if idx < 0 {
			t.Fatalf("render missing %q:\n%s", line, out)
		}
		rest := out[idx+len(line):]
		end := strings.IndexByte(rest, '\n')
		if end < 0 {
			end = len(rest)
		}
		var value uint64
		if _, err := fmt.Sscanf(rest[:end], "%d", &value); err != nil {
			t.Fatalf("bucket le=%q value %q is not an integer: %v", bound, rest[:end], err)
		}
		got[bound] = value
		if fmt.Sprint(value) != want[bound] {
			t.Errorf("bucket le=%q = %d, want %s", bound, value, want[bound])
		}
	}

	// Cumulative buckets must be monotonic and never exceed count.
	order := []string{"50", "100", "150", "200", "500", "1000", "2000", "+Inf"}
	for i := 1; i < len(order); i++ {
		if got[order[i]] < got[order[i-1]] {
			t.Fatalf("buckets are not monotonic: %v", got)
		}
	}
	if !strings.Contains(out, fmt.Sprintf("sos_dispatch_latency_ms_count %d", got["+Inf"])) {
		t.Fatalf("+Inf bucket does not match count:\n%s", out)
	}
}

func TestObserveDispatchDurationCountsOneBucket(t *testing.T) {
	m := New()
	m.ObserveDispatchDuration(30)   // le=50
	m.ObserveDispatchDuration(4000) // above every bound: +Inf only
	m.mu.Lock()
	counts, count := append([]uint64(nil), m.bucketCounts...), m.dispatchCount
	m.mu.Unlock()

	total := uint64(0)
	for _, c := range counts {
		total += c
	}
	if total != 1 {
		t.Fatalf("bucket counters sum to %d, want 1 (only the 30 ms observation is bounded)", total)
	}
	if counts[0] != 1 {
		t.Fatalf("le=50 counter = %d, want 1", counts[0])
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}
