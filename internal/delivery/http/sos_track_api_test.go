package http_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
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
}

func (f *fakeWSTrackingAuthorizer) AuthorizeTracking(ctx context.Context, userID, alertID string) error {
	return f.trackingErr
}

func (f *fakeWSTrackingAuthorizer) AddBreadcrumb(ctx context.Context, senderID, alertID string, request *model.BreadcrumbRequest) (*model.BreadcrumbResponse, error) {
	if f.breadcrumbs != nil {
		select {
		case f.breadcrumbs <- request:
		default:
		}
	}
	return &model.BreadcrumbResponse{}, nil
}

func newSOSTrackTestServer(t *testing.T, verifier *fakeWSTokenVerifier, authorizer *fakeWSTrackingAuthorizer, hub *tracking.Hub, authTimeout time.Duration) string {
	t.Helper()

	log := logrus.New()
	log.SetOutput(io.Discard)

	controller := deliveryhttp.NewSOSTrackController(authorizer, verifier, hub, log)
	controller.AuthTimeout = authTimeout

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
