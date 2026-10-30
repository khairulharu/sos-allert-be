package http

import (
	"context"
	"sync"
	"time"

	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/usecase"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

// SOSTrackController serves WS /ws/sos/:alertId/track.
// Auth is the raw session token in ?token= (or the Authorization header),
// verified the same way as the HTTP middleware. The sender sends
// breadcrumb JSON; responders receive LOCATION_UPDATE broadcasts.
type SOSTrackController struct {
	Log         *logrus.Logger
	UseCase     *usecase.TriggerSOSUseCase
	UserUseCase *usecase.UserUseCase
	Hub         usecase.TrackHub
}

func NewSOSTrackController(useCase *usecase.TriggerSOSUseCase, userUseCase *usecase.UserUseCase, hub usecase.TrackHub, log *logrus.Logger) *SOSTrackController {
	return &SOSTrackController{Log: log, UseCase: useCase, UserUseCase: userUseCase, Hub: hub}
}

// Handler returns the Fiber WebSocket handler. It must be registered
// before the auth middleware, because WebSocket auth uses ?token=.
func (c *SOSTrackController) Handler() fiber.Handler {
	return websocket.New(func(conn *websocket.Conn) {
		alertID := conn.Params("alertId")
		token := conn.Query("token")
		if token == "" {
			token = conn.Headers("Authorization")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		auth, err := c.UserUseCase.Verify(ctx, &model.VerifyUserRequest{Token: token})
		cancel()
		if err != nil {
			_ = conn.WriteJSON(fiber.Map{"error": "unauthorized"})
			_ = conn.Close()
			return
		}
		userID := auth.ID
		if err := c.UseCase.AuthorizeTracking(context.Background(), userID, alertID); err != nil {
			_ = conn.WriteJSON(fiber.Map{"error": err.Error()})
			_ = conn.Close()
			return
		}
		// Subscribe before the read loop so no broadcast is missed.
		stop := make(chan struct{})
		if c.Hub != nil {
			sub, unsub := c.Hub.Subscribe(alertID)
			defer unsub()
			var writeMu sync.Mutex
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
			if _, err := c.UseCase.AddBreadcrumb(context.Background(), userID, alertID, &req); err != nil {
				c.Log.WithError(err).Warn("websocket breadcrumb rejected")
				_ = conn.WriteJSON(fiber.Map{"error": err.Error()})
				continue
			}
		}
		_ = conn.Close()
	})
}
