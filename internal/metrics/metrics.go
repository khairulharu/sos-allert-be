package metrics

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gofiber/fiber/v2"
)

// Metrics implements the three README observability signals without a
// new dependency: dispatch latency histogram, push failure counter, and
// active-incident gauge. It renders Prometheus text exposition format.
// OpenTelemetry tracing is intentionally not included: it needs an OTEL
// SDK + exporter decision and credentials, and is documented in README
// section 2 as a follow-up.
type Metrics struct {
	mu            sync.Mutex
	buckets       []float64
	bucketCounts  []uint64
	dispatchCount uint64
	dispatchSumMs float64
	pushFailures  uint64
	activeSOS     int64
}

var latencyBuckets = []float64{50, 100, 150, 200, 500, 1000, 2000}

// New returns a Metrics collector.
func New() *Metrics {
	return &Metrics{buckets: latencyBuckets, bucketCounts: make([]uint64, len(latencyBuckets))}
}

// ObserveDispatchDuration records one POST /api/sos dispatch latency.
func (m *Metrics) ObserveDispatchDuration(ms float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dispatchCount++
	m.dispatchSumMs += ms
	for i, b := range m.buckets {
		if ms <= b {
			m.bucketCounts[i]++
		}
	}
}

// IncPushFailures records a failed push/SMS delivery attempt.
func (m *Metrics) IncPushFailures() {
	atomic.AddUint64(&m.pushFailures, 1)
}

// SetActiveSOS sets the active-incident gauge.
func (m *Metrics) SetActiveSOS(n int64) {
	atomic.StoreInt64(&m.activeSOS, n)
}

// Handler serves GET /metrics in Prometheus text format.
func (m *Metrics) Handler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		return c.SendString(m.Render())
	}
}

// Render returns the exposition text. Exported for tests.
func (m *Metrics) Render() string {
	m.mu.Lock()
	count, sum, buckets := m.dispatchCount, m.dispatchSumMs, append([]uint64(nil), m.bucketCounts...)
	m.mu.Unlock()
	failures := atomic.LoadUint64(&m.pushFailures)
	active := atomic.LoadInt64(&m.activeSOS)

	var b strings.Builder
	b.WriteString("# HELP sos_dispatch_latency_ms Dispatch latency for POST /api/sos in milliseconds.\n")
	b.WriteString("# TYPE sos_dispatch_latency_ms histogram\n")
	cumulative := uint64(0)
	// Buckets are cumulative in Prometheus; our counts are per-bucket
	// observations, so accumulate in order.
	// bucketCounts were recorded in latencyBuckets order (already sorted).
	for i, bound := range m.buckets {
		cumulative += buckets[i]
		fmt.Fprintf(&b, "sos_dispatch_latency_ms_bucket{le=\"%g\"} %d\n", bound, cumulative)
	}
	fmt.Fprintf(&b, "sos_dispatch_latency_ms_bucket{le=\"+Inf\"} %d\n", count)
	fmt.Fprintf(&b, "sos_dispatch_latency_ms_sum %g\n", sum)
	fmt.Fprintf(&b, "sos_dispatch_latency_ms_count %d\n", count)
	b.WriteString("# HELP push_delivery_failures_total Failed push/SMS delivery attempts.\n")
	b.WriteString("# TYPE push_delivery_failures_total counter\n")
	fmt.Fprintf(&b, "push_delivery_failures_total %d\n", failures)
	b.WriteString("# HELP active_sos_count Currently active SOS incidents.\n")
	b.WriteString("# TYPE active_sos_count gauge\n")
	fmt.Fprintf(&b, "active_sos_count %d\n", active)
	return b.String()
}
