package metrics

import (
	"strings"
	"testing"
)

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
