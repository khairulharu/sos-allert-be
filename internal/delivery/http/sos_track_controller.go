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
	// wsBreadcrumbTimeout bounds one breadcrumb write triggered by a frame,
	// so a slow database cannot pin the socket (or block shutdown).
	wsBreadcrumbTimeout = 5 * time.Second
	// DefaultWSWriteTimeout bounds every frame the server writes: a client
	// that stops reading must not block a broadcast goroutine forever
	// (#42).
	DefaultWSWriteTimeout = 5 * time.Second
	// defaultWSPongWait is how long a client may stay silent (no pong, no
	// frame) before the read deadline drops it.
	defaultWSPongWait = 60 * time.Second
	// defaultWSPingInterval is how often the server pings an idle socket.
	defaultWSPingInterval = 25 * time.Second
	// wsPermitLocal carries the admitted permit from the HTTP handler (which
	// enforces the pre-upgrade budgets) into the socket session.
	wsPermitLocal = "sos.ws.permit"

	// wsRetryAfterSeconds is the Retry-After hint on a rejected upgrade.
	wsRetryAfterSeconds = "5"
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
//
// Hardening (#42): the route sits outside the auth middleware and the HTTP
// rate limiter, so it enforces its own budgets (global + per IP before the
// upgrade, per user after auth), a write deadline on every frame, a
// ping/pong read deadline that drops dead clients, and bounded contexts for
// the authorize and breadcrumb database calls.
type SOSTrackController struct {
	Log         *logrus.Logger
	UseCase     TrackingAuthorizer
	UserUseCase TokenVerifier
	Hub         usecase.TrackHub
	// AuthTimeout overrides defaultWSAuthTimeout; zero uses the default.
	AuthTimeout time.Duration
	// WriteTimeout bounds one frame write (default DefaultWSWriteTimeout).
	WriteTimeout time.Duration
	// PongWait / PingInterval tune the keepalive (defaults 60 s / 25 s).
	PongWait     time.Duration
	PingInterval time.Duration
	// Limiter bounds concurrent sockets; nil uses defaultWSLimiter.
	Limiter *WSConnectionLimiter
}

func NewSOSTrackController(useCase TrackingAuthorizer, userUseCase TokenVerifier, hub usecase.TrackHub, log *logrus.Logger) *SOSTrackController {
	return &SOSTrackController{
		Log:         log,
		UseCase:     useCase,
		UserUseCase: userUseCase,
		Hub:         hub,
		Limiter:     NewWSConnectionLimiter(0, 0, 0),
	}
}

func (c *SOSTrackController) limiter() *WSConnectionLimiter {
	if c.Limiter != nil {
		return c.Limiter
	}
	return defaultWSLimiter
}

func (c *SOSTrackController) writeTimeout() time.Duration {
	if c.WriteTimeout > 0 {
		return c.WriteTimeout
	}
	return DefaultWSWriteTimeout
}

func (c *SOSTrackController) pongWait() time.Duration {
	if c.PongWait > 0 {
		return c.PongWait
	}
	return defaultWSPongWait
}

func (c *SOSTrackController) pingInterval() time.Duration {
	if c.PingInterval > 0 {
		return c.PingInterval
	}
	return defaultWSPingInterval
}

// Handler returns the Fiber WebSocket handler. It must be registered before
// the auth middleware: browsers cannot set an Authorization header on the
// handshake, so this handler verifies the session itself. The connection
// budget is checked here, before the upgrade, and rejected with 429.
func (c *SOSTrackController) Handler() fiber.Handler {
	upgrade := websocket.New(c.handle)
	return func(ctx *fiber.Ctx) error {
		permit, err := c.limiter().Acquire(ctx.IP())
		if err != nil {
			c.Log.WithError(err).Warnf("websocket upgrade rejected for %s", ctx.IP())
			ctx.Set(fiber.HeaderRetryAfter, wsRetryAfterSeconds)
			return fiber.NewError(fiber.StatusTooManyRequests, ErrWSConnectionLimit.Error())
		}
		// The session copies the locals, so the permit travels with it.
		ctx.Locals(wsPermitLocal, permit)

		if err := upgrade(ctx); err != nil {
			// The handshake never became a socket (e.g. not an upgrade), so
			// the session never ran: hand the slots back here.
			permit.Release()
			return err
		}
		return nil
	}
}

// handle runs one authenticated tracking session.
func (c *SOSTrackController) handle(conn *websocket.Conn) {
	permit, _ := conn.Locals(wsPermitLocal).(*WSConnectionPermit)
	if permit != nil {
		defer permit.Release()
	}
	alertID := conn.Params("alertId")

	// One writer at a time: the broadcast goroutine, the keepalive and the
	// read loop all write to the same connection, and every write is
	// deadline-bounded so a client that stops reading is dropped.
	var writeMu sync.Mutex
	writeMessage := func(messageType int, payload []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := conn.SetWriteDeadline(time.Now().Add(c.writeTimeout())); err != nil {
			return err
		}
		return conn.WriteMessage(messageType, payload)
	}
	writeJSON := func(value any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := conn.SetWriteDeadline(time.Now().Add(c.writeTimeout())); err != nil {
			return
		}
		_ = conn.WriteJSON(value)
	}
	reject := func(message string) {
		writeJSON(fiber.Map{"error": message})
		_ = writeMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, message))
		_ = conn.Close()
	}

	token, err := c.sessionToken(conn)
	if err != nil {
		c.Log.WithError(err).Warn("websocket auth rejected")
		writeJSON(fiber.Map{"error": "unauthorized"})
		_ = conn.Close()
		return
	}

	verifyCtx, cancelVerify := context.WithTimeout(context.Background(), wsVerifyTimeout)
	auth, err := c.UserUseCase.Verify(verifyCtx, &model.VerifyUserRequest{Token: token})
	cancelVerify()
	if err != nil {
		c.Log.WithError(err).Warn("websocket session rejected")
		writeJSON(fiber.Map{"error": "unauthorized"})
		_ = conn.Close()
		return
	}

	// Per-user budget: the identity is only known after the in-band auth.
	if permit != nil {
		if err := permit.ClaimUser(auth.ID); err != nil {
			c.Log.WithError(err).Warnf("websocket per-user limit reached for %s", auth.ID)
			reject(ErrWSConnectionLimit.Error())
			return
		}
	}

	authorizeCtx, cancelAuthorize := context.WithTimeout(context.Background(), wsVerifyTimeout)
	err = c.UseCase.AuthorizeTracking(authorizeCtx, auth.ID, alertID)
	cancelAuthorize()
	if err != nil {
		writeJSON(fiber.Map{"error": err.Error()})
		_ = conn.Close()
		return
	}

	// Keepalive: the client must answer pings (the browser does it
	// automatically) or the read deadline closes the socket, so a
	// half-open connection cannot hold a slot forever.
	pongWait := c.pongWait()
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

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

	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		ping := time.NewTicker(c.pingInterval())
		defer ping.Stop()
		for {
			select {
			case <-ping.C:
				if err := writeMessage(websocket.PingMessage, nil); err != nil {
					_ = conn.Close()
					return
				}
			case <-stop:
				return
			}
		}
	}()
	if sub != nil {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for {
				select {
				case payload, ok := <-sub:
					if !ok {
						return
					}
					if err := writeMessage(websocket.TextMessage, payload); err != nil {
						_ = conn.Close()
						return
					}
				case <-stop:
					return
				}
			}
		}()
	}
	defer func() {
		close(stop)
		writers.Wait()
	}()

	// Read loop: only the sender's breadcrumbs are accepted;
	// AddBreadcrumb enforces ownership and active status.
	for {
		var req model.BreadcrumbRequest
		if err := conn.ReadJSON(&req); err != nil {
			break
		}
		breadcrumbCtx, cancelBreadcrumb := context.WithTimeout(context.Background(), wsBreadcrumbTimeout)
		_, err := c.UseCase.AddBreadcrumb(breadcrumbCtx, auth.ID, alertID, &req)
		cancelBreadcrumb()
		if err != nil {
			c.Log.WithError(err).Warn("websocket breadcrumb rejected")
			writeJSON(fiber.Map{"error": err.Error()})
			continue
		}
	}
	_ = conn.Close()
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
