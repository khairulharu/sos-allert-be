package http

import (
	"errors"
	"sync"
)

// ErrWSConnectionLimit is returned when a live-tracking socket would push the
// process, a client IP or a user over its concurrency budget.
var ErrWSConnectionLimit = errors.New("too many live-tracking connections, retry shortly")

// Default concurrency budgets for WS /ws/sos/:alertId/track. They are
// deliberately small: one browser tab needs one socket, and the stream is
// only useful while an incident is open.
const (
	DefaultWSMaxConnections = 500
	DefaultWSMaxPerIP       = 10
	DefaultWSMaxPerUser     = 3
)

// WSConnectionLimiter bounds concurrent tracking sockets (#42). The endpoint
// is registered outside the auth middleware and the HTTP rate limiter, so
// without a bound a client could open sockets until the process ran out of
// file descriptors.
//
// The global and per-IP limits are enforced before the upgrade (the IP is
// known from the handshake); the per-user limit is claimed once the in-band
// auth frame has been verified, because a browser cannot send an
// Authorization header with the handshake.
type WSConnectionLimiter struct {
	mu         sync.Mutex
	maxTotal   int
	maxPerIP   int
	maxPerUser int
	total      int
	byIP       map[string]int
	byUser     map[string]int
}

// NewWSConnectionLimiter builds a limiter. Non-positive values fall back to
// the defaults above.
func NewWSConnectionLimiter(maxTotal, maxPerIP, maxPerUser int) *WSConnectionLimiter {
	if maxTotal <= 0 {
		maxTotal = DefaultWSMaxConnections
	}
	if maxPerIP <= 0 {
		maxPerIP = DefaultWSMaxPerIP
	}
	if maxPerUser <= 0 {
		maxPerUser = DefaultWSMaxPerUser
	}
	return &WSConnectionLimiter{
		maxTotal:   maxTotal,
		maxPerIP:   maxPerIP,
		maxPerUser: maxPerUser,
		byIP:       map[string]int{},
		byUser:     map[string]int{},
	}
}

// WSConnectionPermit is one admitted socket. Release is idempotent so the
// upgrade path (refused handshake) and the session path can both call it.
type WSConnectionPermit struct {
	limiter *WSConnectionLimiter
	ip      string
	user    string
	once    sync.Once
}

// Acquire reserves a global + per-IP slot for the connection. It returns
// ErrWSConnectionLimit when a budget is exhausted, so the caller can answer
// 429 before upgrading.
func (l *WSConnectionLimiter) Acquire(ip string) (*WSConnectionPermit, error) {
	if l == nil {
		return &WSConnectionPermit{}, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.maxTotal {
		return nil, ErrWSConnectionLimit
	}
	if l.byIP[ip] >= l.maxPerIP {
		return nil, ErrWSConnectionLimit
	}
	l.total++
	l.byIP[ip]++
	return &WSConnectionPermit{limiter: l, ip: ip}, nil
}

// ClaimUser reserves the authenticated user's slot. A second socket over the
// per-user budget is refused with ErrWSConnectionLimit; the caller closes the
// socket with 1013 (try again later). Claiming the same user twice on one
// permit is a no-op.
func (p *WSConnectionPermit) ClaimUser(userID string) error {
	if p == nil || p.limiter == nil {
		return nil
	}
	if p.user == userID {
		return nil
	}
	if userID == "" {
		// No identity to budget against: the global/IP slots already apply.
		return nil
	}
	p.limiter.mu.Lock()
	defer p.limiter.mu.Unlock()
	if p.limiter.byUser[userID] >= p.limiter.maxPerUser {
		return ErrWSConnectionLimit
	}
	if p.user != "" {
		// Switching identity releases the previous claim.
		p.limiter.releaseUserLocked(p.user)
	}
	p.limiter.byUser[userID]++
	p.user = userID
	return nil
}

// Release frees every slot this permit holds. It is safe to call twice.
func (p *WSConnectionPermit) Release() {
	if p == nil || p.limiter == nil {
		return
	}
	p.once.Do(func() {
		p.limiter.mu.Lock()
		defer p.limiter.mu.Unlock()
		p.limiter.releaseUserLocked(p.user)
		if p.ip != "" {
			if n := p.limiter.byIP[p.ip]; n <= 1 {
				delete(p.limiter.byIP, p.ip)
			} else {
				p.limiter.byIP[p.ip] = n - 1
			}
		}
		if p.limiter.total > 0 {
			p.limiter.total--
		}
	})
}

func (l *WSConnectionLimiter) releaseUserLocked(userID string) {
	if userID == "" {
		return
	}
	if n := l.byUser[userID]; n <= 1 {
		delete(l.byUser, userID)
	} else {
		l.byUser[userID] = n - 1
	}
}

// Stats reports the current totals; used by tests and useful in logs.
func (l *WSConnectionLimiter) Stats() (total, perUserMax int) {
	if l == nil {
		return 0, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	largest := 0
	for _, n := range l.byUser {
		if n > largest {
			largest = n
		}
	}
	return l.total, largest
}

// defaultWSLimiter backs a controller constructed without an explicit
// limiter (tests, tools), so the endpoint is never unbounded.
var defaultWSLimiter = NewWSConnectionLimiter(0, 0, 0)
