package http

import (
	"errors"
	"testing"
)

// The tracking socket is registered outside the auth middleware and the HTTP
// rate limiter, so its own budget must hold at all three levels (#42).
func TestWSConnectionLimiterGlobalBudget(t *testing.T) {
	limiter := NewWSConnectionLimiter(2, 10, 10)

	first, err := limiter.Acquire("10.0.0.1")
	if err != nil {
		t.Fatalf("first acquire error = %v", err)
	}
	second, err := limiter.Acquire("10.0.0.2")
	if err != nil {
		t.Fatalf("second acquire error = %v", err)
	}
	if _, err := limiter.Acquire("10.0.0.3"); !errors.Is(err, ErrWSConnectionLimit) {
		t.Fatalf("third acquire error = %v, want ErrWSConnectionLimit", err)
	}

	first.Release()
	if third, err := limiter.Acquire("10.0.0.3"); err != nil {
		t.Fatalf("acquire after release error = %v", err)
	} else {
		third.Release()
	}
	second.Release()

	// Releases are idempotent: a double release must not free someone
	// else's slot (the upgrade path and the session path may both run).
	first.Release()
	if total, _ := limiter.Stats(); total != 0 {
		t.Fatalf("total after releases = %d, want 0", total)
	}
}

func TestWSConnectionLimiterPerIPBudget(t *testing.T) {
	limiter := NewWSConnectionLimiter(100, 2, 100)

	a, err := limiter.Acquire("10.0.0.1")
	if err != nil {
		t.Fatalf("first acquire error = %v", err)
	}
	b, err := limiter.Acquire("10.0.0.1")
	if err != nil {
		t.Fatalf("second acquire error = %v", err)
	}
	if _, err := limiter.Acquire("10.0.0.1"); !errors.Is(err, ErrWSConnectionLimit) {
		t.Fatalf("third acquire from the same IP error = %v, want the per-IP limit", err)
	}
	// Another IP is unaffected.
	if c, err := limiter.Acquire("10.0.0.2"); err != nil {
		t.Fatalf("other IP acquire error = %v", err)
	} else {
		c.Release()
	}
	a.Release()
	b.Release()
}

func TestWSConnectionLimiterPerUserBudget(t *testing.T) {
	limiter := NewWSConnectionLimiter(100, 100, 2)

	first, _ := limiter.Acquire("10.0.0.1")
	second, _ := limiter.Acquire("10.0.0.2")
	third, _ := limiter.Acquire("10.0.0.3")

	if err := first.ClaimUser("user-1"); err != nil {
		t.Fatalf("first ClaimUser error = %v", err)
	}
	if err := second.ClaimUser("user-1"); err != nil {
		t.Fatalf("second ClaimUser error = %v", err)
	}
	if err := third.ClaimUser("user-1"); !errors.Is(err, ErrWSConnectionLimit) {
		t.Fatalf("third ClaimUser error = %v, want ErrWSConnectionLimit", err)
	}
	// A different user is unaffected.
	if err := third.ClaimUser("user-2"); err != nil {
		t.Fatalf("other user ClaimUser error = %v", err)
	}
	// Claiming twice on one permit is a no-op, not a second slot.
	if err := third.ClaimUser("user-2"); err != nil {
		t.Fatalf("repeated ClaimUser error = %v", err)
	}
	if total, _ := limiter.Stats(); total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}

	// Releasing frees both the IP and the user slot.
	first.Release()
	permit, _ := limiter.Acquire("10.0.0.4")
	if err := permit.ClaimUser("user-1"); err != nil {
		t.Fatalf("ClaimUser after release error = %v, want a free slot", err)
	}
	permit.Release()

	second.Release()
	third.Release()
	if total, largest := limiter.Stats(); total != 0 || largest != 0 {
		t.Fatalf("stats after release = %d/%d, want 0/0", total, largest)
	}
}

// A permit with no identity (native client without a session? never in
// practice) must not consume a per-user slot.
func TestWSConnectionPermitEmptyUserIsUnmetered(t *testing.T) {
	limiter := NewWSConnectionLimiter(1, 1, 1)
	permit, err := limiter.Acquire("10.0.0.1")
	if err != nil {
		t.Fatalf("acquire error = %v", err)
	}
	defer permit.Release()
	if err := permit.ClaimUser(""); err != nil {
		t.Fatalf("ClaimUser(\"\") error = %v", err)
	}
	if _, largest := limiter.Stats(); largest != 0 {
		t.Fatalf("largest per-user count = %d, want 0", largest)
	}
}

func TestWSConnectionLimiterDefaults(t *testing.T) {
	limiter := NewWSConnectionLimiter(0, 0, 0)
	if limiter.maxTotal != DefaultWSMaxConnections ||
		limiter.maxPerIP != DefaultWSMaxPerIP ||
		limiter.maxPerUser != DefaultWSMaxPerUser {
		t.Fatalf("defaults = %d/%d/%d, want %d/%d/%d", limiter.maxTotal, limiter.maxPerIP, limiter.maxPerUser,
			DefaultWSMaxConnections, DefaultWSMaxPerIP, DefaultWSMaxPerUser)
	}

	// nil-safe: a controller built without a limiter still admits (the
	// package-level default limiter applies), and a bare permit is harmless.
	var nilLimiter *WSConnectionLimiter
	permit, err := nilLimiter.Acquire("10.0.0.1")
	if err != nil || permit == nil {
		t.Fatalf("nil limiter acquire = %v, %v", permit, err)
	}
	permit.ClaimUser("user-1")
	permit.Release()
	permit.Release()
	if total, _ := nilLimiter.Stats(); total != 0 {
		t.Fatalf("nil limiter total = %d, want 0", total)
	}
	if defaultWSLimiter == nil || defaultWSLimiter.maxTotal != DefaultWSMaxConnections {
		t.Fatal("defaultWSLimiter is not wired to the defaults")
	}
}
