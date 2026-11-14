package http

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/usecase"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

const (
	// defaultWSAuthTimeout bounds how long a client may take to send the
	// first auth frame after the socket opens.
	defaultWSAuthTimeout = 10 * time.Second
	wsVerifyTimeout      = 5 * time.Second
)

// errWSAuthRequired is returned when the first frame is not an auth frame.
var errWSAuthRequired = errors.New("first frame must be {\"type\":\"auth\",\"token\":\"...\"}")

// TokenVerifier resolves a raw session token to the authenticated user.
// Satisfied by *usecase.UserUseCase.
type TokenVerifier interface {
	Verify(ctx context.Context, request *model.VerifyUserRequest) (*model.Auth, error)
}

// TrackingAuthorizer guards an incident's live stream and accepts the
// sender's breadcrumb frames. Satisfied by *usecase.TriggerSOSUseCase.
type TrackingAuthorizer interface {
	AuthorizeTracking(ctx context.Context, userID, alertID string) error
	AddBreadcrumb(ctx context.Context, senderID, alertID string, request *model.BreadcrumbRequest) (*model.BreadcrumbResponse, error)
}

// SOSTrackController serves WS /ws/sos/:alertId/track.
//
// Authentication never travels in the URL: a browser opens the socket and
// sends {"type":"auth","token":"..."} as the first frame, while native
// clients may send the raw token in the Authorization header. The server
// answers {"type":"auth_ok"} once the session and alert participation are
// verified; the sender then sends breadcrumb JSON and responders receive
// LOCATION_UPDATE broadcasts.
type SOSTrackController struct {
	Log         *logrus.Logger
	UseCase     TrackingAuthorizer
	UserUseCase TokenVerifier
	Hub         usecase.TrackHub
	// AuthTimeout overrides defaultWSAuthTimeout; zero uses the default.
	AuthTimeout time.Duration
}

func NewSOSTrackController(useCase TrackingAuthorizer, userUseCase TokenVerifier, hub usecase.TrackHub, log *logrus.Logger) *SOSTrackController {
	return &SOSTrackController{Log: log, UseCase: useCase, UserUseCase: userUseCase, Hub: hub}
}

// Handler returns the Fiber WebSocket handler. It must be registered before
// the auth middleware: browsers cannot set an Authorization header on the
// handshake, so this handler verifies the session itself.
func (c *SOSTrackController) Handler() fiber.Handler {
	return websocket.New(func(conn *websocket.Conn) {
		alertID := conn.Params("alertId")

		token, err := c.sessionToken(conn)
		if err != nil {
			c.Log.WithError(err).Warn("websocket auth rejected")
			_ = conn.WriteJSON(fiber.Map{"error": "unauthorized"})
			_ = conn.Close()
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), wsVerifyTimeout)
		auth, err := c.UserUseCase.Verify(ctx, &model.VerifyUserRequest{Token: token})
		cancel()
		if err != nil {
			c.Log.WithError(err).Warn("websocket session rejected")
			_ = conn.WriteJSON(fiber.Map{"error": "unauthorized"})
			_ = conn.Close()
			return
		}

		if err := c.UseCase.AuthorizeTracking(context.Background(), auth.ID, alertID); err != nil {
			_ = conn.WriteJSON(fiber.Map{"error": err.Error()})
			_ = conn.Close()
			return
		}

		// One writer at a time: the broadcast goroutine and the read loop
		// both write to the same connection.
		var writeMu sync.Mutex
		writeJSON := func(value any) {
			writeMu.Lock()
			defer writeMu.Unlock()
			_ = conn.WriteJSON(value)
		}

		// Subscribe before auth_ok so no broadcast is missed; the client
		// flushes queued breadcrumbs only after it sees auth_ok.
		var sub <-chan []byte
		stop := make(chan struct{})
		if c.Hub != nil {
			var unsub func()
			sub, unsub = c.Hub.Subscribe(alertID)
			defer unsub()
		}

		writeJSON(fiber.Map{"type": "auth_ok"})

		if sub != nil {
			go func() {
				for {
					select {
					case payload, ok := <-sub:
						if !ok {
							return
						}
						writeMu.Lock()
						_ = conn.WriteMessage(websocket.TextMessage, payload)
						writeMu.Unlock()
					case <-stop:
						return
					}
				}
			}()
		}
		defer close(stop)

		// Read loop: only the sender's breadcrumbs are accepted;
		// AddBreadcrumb enforces ownership and active status.
		for {
			var req model.BreadcrumbRequest
			if err := conn.ReadJSON(&req); err != nil {
				break
			}
			if _, err := c.UseCase.AddBreadcrumb(context.Background(), auth.ID, alertID, &req); err != nil {
				c.Log.WithError(err).Warn("websocket breadcrumb rejected")
				writeJSON(fiber.Map{"error": err.Error()})
				continue
			}
		}
		_ = conn.Close()
	})
}

// sessionToken returns the raw session token from the Authorization header,
// or from the mandatory first frame. The URL query is intentionally ignored.
func (c *SOSTrackController) sessionToken(conn *websocket.Conn) (string, error) {
	if token := strings.TrimSpace(conn.Headers("Authorization")); token != "" {
		return token, nil
	}

	timeout := c.AuthTimeout
	if timeout <= 0 {
		timeout = defaultWSAuthTimeout
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}

	var frame model.WSAuthRequest
	if err := conn.ReadJSON(&frame); err != nil {
		return "", err
	}
	if frame.Type != "auth" {
		return "", errWSAuthRequired
	}
	token := strings.TrimSpace(frame.Token)
	if token == "" {
		return "", errWSAuthRequired
	}

	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return "", err
	}
	return token, nil
}
