package http_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	deliveryhttp "golang-clean-architecture/internal/delivery/http"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/tracking"

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

type fakeWSTokenVerifier struct {
	tokens map[string]string
}

func (f *fakeWSTokenVerifier) Verify(ctx context.Context, request *model.VerifyUserRequest) (*model.Auth, error) {
	userID, ok := f.tokens[request.Token]
	if !ok {
		return nil, fiber.ErrUnauthorized
	}
	return &model.Auth{ID: userID}, nil
}

type fakeWSTrackingAuthorizer struct {
	trackingErr error
	breadcrumbs chan *model.BreadcrumbRequest

	// Deadline plumbing (#42): the controller must bound both database
	// calls, so the fake records whether the context carried a deadline.
	authorizeDeadline  time.Duration
	breadcrumbDeadline time.Duration
}

func (f *fakeWSTrackingAuthorizer) AuthorizeTracking(ctx context.Context, userID, alertID string) error {
	f.authorizeDeadline = remainingDeadline(ctx)
	return f.trackingErr
}

func (f *fakeWSTrackingAuthorizer) AddBreadcrumb(ctx context.Context, senderID, alertID string, request *model.BreadcrumbRequest) (*model.BreadcrumbResponse, error) {
	f.breadcrumbDeadline = remainingDeadline(ctx)
	if f.breadcrumbs != nil {
		select {
		case f.breadcrumbs <- request:
		default:
		}
	}
	return &model.BreadcrumbResponse{}, nil
}

// remainingDeadline reports how long the context still has, or -1 when it
// carries no deadline at all.
func remainingDeadline(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return -1
	}
	return time.Until(deadline)
}

func newSOSTrackTestServer(t *testing.T, verifier *fakeWSTokenVerifier, authorizer *fakeWSTrackingAuthorizer, hub *tracking.Hub, authTimeout time.Duration) string {
	t.Helper()
	return newSOSTrackTestServerWith(t, verifier, authorizer, hub, func(controller *deliveryhttp.SOSTrackController) {
		controller.AuthTimeout = authTimeout
	})
}

// newSOSTrackTestServerWith also lets a test tune the controller (limits,
// deadlines, keepalive).
func newSOSTrackTestServerWith(t *testing.T, verifier *fakeWSTokenVerifier, authorizer *fakeWSTrackingAuthorizer, hub *tracking.Hub, tune func(*deliveryhttp.SOSTrackController)) string {
	t.Helper()

	log := logrus.New()
	log.SetOutput(io.Discard)

	controller := deliveryhttp.NewSOSTrackController(authorizer, verifier, hub, log)
	if tune != nil {
		tune(controller)
	}

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/ws/sos/:alertId/track", controller.Handler())

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	go func() { _ = app.Listener(listener) }()
	t.Cleanup(func() { _ = app.Shutdown() })

	return "ws://" + listener.Addr().String() + "/ws/sos/alert-1/track"
}

func dialSOSTrack(t *testing.T, url string, header http.Header) *websocket.Conn {
	t.Helper()

	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial error = %v (status %d)", err, status)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readWSJSON(t *testing.T, conn *websocket.Conn, out any) {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline = %v", err)
	}
	if err := conn.ReadJSON(out); err != nil {
		t.Fatalf("read error = %v", err)
	}
}

func TestSOSTrackFirstFrameAuthThenBroadcast(t *testing.T) {
	hub := tracking.NewHub()
	authorizer := &fakeWSTrackingAuthorizer{breadcrumbs: make(chan *model.BreadcrumbRequest, 1)}
	verifier := &fakeWSTokenVerifier{tokens: map[string]string{"good-token": "user-1"}}
	url := newSOSTrackTestServer(t, verifier, authorizer, hub, time.Second)

	conn := dialSOSTrack(t, url, nil)
	if err := conn.WriteJSON(model.WSAuthRequest{Type: "auth", Token: "good-token"}); err != nil {
		t.Fatalf("write auth frame = %v", err)
	}

	var ack map[string]string
	readWSJSON(t, conn, &ack)
	if ack["type"] != "auth_ok" {
		t.Fatalf("first server frame = %#v, want auth_ok", ack)
	}

	hub.Publish("alert-1", []byte(`{"type":"LOCATION_UPDATE","alert_id":"alert-1"}`))
	deadline := time.Now().Add(3 * time.Second)
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set read deadline = %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("broadcast read error = %v", err)
	}
	var update map[string]string
	if err := json.Unmarshal(payload, &update); err != nil {
		t.Fatalf("broadcast json = %v", err)
	}
	if update["type"] != "LOCATION_UPDATE" {
		t.Fatalf("broadcast = %s", payload)
	}

	latitude, longitude := -6.2, 106.8
	if err := conn.WriteJSON(&model.BreadcrumbRequest{Latitude: &latitude, Longitude: &longitude}); err != nil {
		t.Fatalf("write breadcrumb = %v", err)
	}
	select {
	case req := <-authorizer.breadcrumbs:
		if req.Latitude == nil || *req.Latitude != latitude {
			t.Fatalf("breadcrumb latitude = %v", req.Latitude)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("breadcrumb never reached the use case")
	}
}

func TestSOSTrackAcceptsAuthorizationHeader(t *testing.T) {
	hub := tracking.NewHub()
	authorizer := &fakeWSTrackingAuthorizer{breadcrumbs: make(chan *model.BreadcrumbRequest, 1)}
	verifier := &fakeWSTokenVerifier{tokens: map[string]string{"good-token": "user-1"}}
	url := newSOSTrackTestServer(t, verifier, authorizer, hub, time.Second)

	header := http.Header{}
	header.Set("Authorization", "good-token")
	conn := dialSOSTrack(t, url, header)

	var ack map[string]string
	readWSJSON(t, conn, &ack)
	if ack["type"] != "auth_ok" {
		t.Fatalf("first server frame = %#v, want auth_ok", ack)
	}

	latitude, longitude := -6.2, 106.8
	if err := conn.WriteJSON(&model.BreadcrumbRequest{Latitude: &latitude, Longitude: &longitude}); err != nil {
		t.Fatalf("write breadcrumb = %v", err)
	}
	select {
	case <-authorizer.breadcrumbs:
	case <-time.After(3 * time.Second):
		t.Fatal("breadcrumb never reached the use case")
	}
}

func TestSOSTrackRejectsInvalidToken(t *testing.T) {
	hub := tracking.NewHub()
	authorizer := &fakeWSTrackingAuthorizer{}
	verifier := &fakeWSTokenVerifier{tokens: map[string]string{"good-token": "user-1"}}
	url := newSOSTrackTestServer(t, verifier, authorizer, hub, time.Second)

	conn := dialSOSTrack(t, url, nil)
	if err := conn.WriteJSON(model.WSAuthRequest{Type: "auth", Token: "bad-token"}); err != nil {
		t.Fatalf("write auth frame = %v", err)
	}

	var frame map[string]string
	readWSJSON(t, conn, &frame)
	if frame["error"] == "" {
		t.Fatalf("frame = %#v, want error", frame)
	}
}

func TestSOSTrackRejectsNonAuthFirstFrame(t *testing.T) {
	hub := tracking.NewHub()
	authorizer := &fakeWSTrackingAuthorizer{}
	verifier := &fakeWSTokenVerifier{tokens: map[string]string{"good-token": "user-1"}}
	url := newSOSTrackTestServer(t, verifier, authorizer, hub, time.Second)

	conn := dialSOSTrack(t, url, nil)
	latitude, longitude := -6.2, 106.8
	if err := conn.WriteJSON(&model.BreadcrumbRequest{Latitude: &latitude, Longitude: &longitude}); err != nil {
		t.Fatalf("write breadcrumb = %v", err)
	}

	var frame map[string]string
	readWSJSON(t, conn, &frame)
	if frame["error"] == "" {
		t.Fatalf("frame = %#v, want error", frame)
	}
}

func TestSOSTrackIgnoresQueryToken(t *testing.T) {
	hub := tracking.NewHub()
	authorizer := &fakeWSTrackingAuthorizer{}
	verifier := &fakeWSTokenVerifier{tokens: map[string]string{"good-token": "user-1"}}
	url := newSOSTrackTestServer(t, verifier, authorizer, hub, 100*time.Millisecond)

	conn := dialSOSTrack(t, url+"?token=good-token", nil)
	// No auth frame: the socket must time out unauthenticated even though
	// the old ?token= query would previously have been accepted.
	var frame map[string]string
	readWSJSON(t, conn, &frame)
	if frame["error"] == "" {
		t.Fatalf("frame = %#v, want error", frame)
	}
}

func TestSOSTrackRejectsUnauthorizedAlert(t *testing.T) {
	hub := tracking.NewHub()
	authorizer := &fakeWSTrackingAuthorizer{trackingErr: fiber.ErrForbidden}
	verifier := &fakeWSTokenVerifier{tokens: map[string]string{"good-token": "user-1"}}
	url := newSOSTrackTestServer(t, verifier, authorizer, hub, time.Second)

	conn := dialSOSTrack(t, url, nil)
	if err := conn.WriteJSON(model.WSAuthRequest{Type: "auth", Token: "good-token"}); err != nil {
		t.Fatalf("write auth frame = %v", err)
	}

	var frame map[string]string
	readWSJSON(t, conn, &frame)
	if frame["error"] == "" {
		t.Fatalf("frame = %#v, want error", frame)
	}
}

// waitForWSTotal polls the limiter until it reports the wanted total, so the
// tests do not race the server's session teardown.
func waitForWSTotal(t *testing.T, limiter *deliveryhttp.WSConnectionLimiter, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if total, _ := limiter.Stats(); total == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	total, _ := limiter.Stats()
	t.Fatalf("limiter total = %d, want %d", total, want)
}

// The socket route is not covered by the HTTP rate limiter, so an over-limit
// upgrade is refused with 429 (and a Retry-After hint) before the handshake
// completes (#42).
func TestSOSTrackRejectsOverLimitUpgrade(t *testing.T) {
	hub := tracking.NewHub()
	authorizer := &fakeWSTrackingAuthorizer{}
	verifier := &fakeWSTokenVerifier{tokens: map[string]string{"good-token": "user-1"}}
	limiter := deliveryhttp.NewWSConnectionLimiter(1, 1, 10)
	url := newSOSTrackTestServerWith(t, verifier, authorizer, hub, func(c *deliveryhttp.SOSTrackController) {
		c.Limiter = limiter
	})

	first := dialSOSTrack(t, url, nil)
	if err := first.WriteJSON(model.WSAuthRequest{Type: "auth", Token: "good-token"}); err != nil {
		t.Fatalf("write auth frame = %v", err)
	}
	var ack map[string]string
	readWSJSON(t, first, &ack)
	if ack["type"] != "auth_ok" {
		t.Fatalf("first frame = %#v, want auth_ok", ack)
	}

	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("second dial = nil error, want the upgrade rejected")
	}
	if resp == nil {
		t.Fatalf("second dial error = %v, want an HTTP response with 429", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rejected upgrade status = %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Fatal("429 response has no Retry-After header")
	}

	// Closing the first socket returns the slot, so a later client is
	// admitted again.
	_ = first.Close()
	waitForWSTotal(t, limiter, 0)
	third := dialSOSTrack(t, url, nil)
	if err := third.WriteJSON(model.WSAuthRequest{Type: "auth", Token: "good-token"}); err != nil {
		t.Fatalf("third auth frame = %v", err)
	}
	if err := third.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline = %v", err)
	}
}

// Per-user budget: a second socket for the same authenticated user is closed
// with 1013 after the in-band auth, without ever receiving auth_ok (#42).
func TestSOSTrackRejectsSecondSocketPerUser(t *testing.T) {
	hub := tracking.NewHub()
	authorizer := &fakeWSTrackingAuthorizer{}
	verifier := &fakeWSTokenVerifier{tokens: map[string]string{"good-token": "user-1"}}
	limiter := deliveryhttp.NewWSConnectionLimiter(10, 10, 1)
	url := newSOSTrackTestServerWith(t, verifier, authorizer, hub, func(c *deliveryhttp.SOSTrackController) {
		c.Limiter = limiter
	})

	first := dialSOSTrack(t, url, nil)
	if err := first.WriteJSON(model.WSAuthRequest{Type: "auth", Token: "good-token"}); err != nil {
		t.Fatalf("write auth frame = %v", err)
	}
	var ack map[string]string
	readWSJSON(t, first, &ack)
	if ack["type"] != "auth_ok" {
		t.Fatalf("first frame = %#v, want auth_ok", ack)
	}

	second := dialSOSTrack(t, url, nil)
	if err := second.WriteJSON(model.WSAuthRequest{Type: "auth", Token: "good-token"}); err != nil {
		t.Fatalf("write second auth frame = %v", err)
	}
	_ = second.SetReadDeadline(time.Now().Add(3 * time.Second))
	var frame map[string]string
	if err := second.ReadJSON(&frame); err != nil {
		t.Fatalf("second socket read error = %v, want the limit error frame", err)
	}
	if frame["type"] == "auth_ok" {
		t.Fatalf("second socket = %#v, want the per-user limit to refuse it", frame)
	}
	if !strings.Contains(frame["error"], "too many live-tracking connections") {
		t.Fatalf("second socket error = %q, want the connection-limit message", frame["error"])
	}
}

// Authorize and breadcrumb writes must run on bounded contexts so a slow
// MySQL cannot pin the socket (#42).
func TestSOSTrackBoundsDatabaseCalls(t *testing.T) {
	hub := tracking.NewHub()
	authorizer := &fakeWSTrackingAuthorizer{breadcrumbs: make(chan *model.BreadcrumbRequest, 1)}
	verifier := &fakeWSTokenVerifier{tokens: map[string]string{"good-token": "user-1"}}
	url := newSOSTrackTestServer(t, verifier, authorizer, hub, time.Second)

	conn := dialSOSTrack(t, url, nil)
	if err := conn.WriteJSON(model.WSAuthRequest{Type: "auth", Token: "good-token"}); err != nil {
		t.Fatalf("write auth frame = %v", err)
	}
	var ack map[string]string
	readWSJSON(t, conn, &ack)

	latitude, longitude := -6.2, 106.8
	if err := conn.WriteJSON(&model.BreadcrumbRequest{Latitude: &latitude, Longitude: &longitude}); err != nil {
		t.Fatalf("write breadcrumb = %v", err)
	}
	select {
	case <-authorizer.breadcrumbs:
	case <-time.After(3 * time.Second):
		t.Fatal("breadcrumb never reached the use case")
	}

	if authorizer.authorizeDeadline <= 0 || authorizer.authorizeDeadline > 10*time.Second {
		t.Fatalf("authorize deadline = %v, want a bounded positive deadline", authorizer.authorizeDeadline)
	}
	if authorizer.breadcrumbDeadline <= 0 || authorizer.breadcrumbDeadline > 10*time.Second {
		t.Fatalf("breadcrumb deadline = %v, want a bounded positive deadline", authorizer.breadcrumbDeadline)
	}
}
