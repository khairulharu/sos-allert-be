package route

import (
	"golang-clean-architecture/internal/delivery/http"

	"github.com/gofiber/fiber/v2"
)

type RouteConfig struct {
	App                        *fiber.App
	UserController             *http.UserController
	ContactController          *http.ContactController
	AddressController          *http.AddressController
	LocationController         *http.LocationController
	SOSController              *http.SOSController
	EmergencyContactController *http.EmergencyContactController
	PushController             *http.PushController
	SOSTrackController         *http.SOSTrackController
	MetricsHandler             fiber.Handler
	AuthMiddleware             fiber.Handler
	AuthRateLimit              fiber.Handler
}

func (c *RouteConfig) Setup() {
	c.SetupGuestRoute()
	c.SetupAuthRoute()
}

func (c *RouteConfig) SetupGuestRoute() {
	authRateLimit := c.AuthRateLimit
	if authRateLimit == nil {
		authRateLimit = func(ctx *fiber.Ctx) error { return ctx.Next() }
	}

	c.App.Post("/api/users", authRateLimit, c.UserController.Register)
	c.App.Post("/api/users/_login", authRateLimit, c.UserController.Login)
	if c.MetricsHandler != nil {
		c.App.Get("/metrics", c.MetricsHandler)
	}
	// WebSocket tracking authenticates in-band (first frame, or the
	// Authorization header for native clients), so it is registered before
	// the header-based auth middleware.
	if c.SOSTrackController != nil {
		c.App.Get("/ws/sos/:alertId/track", c.SOSTrackController.Handler())
	}
}

func (c *RouteConfig) SetupAuthRoute() {
	c.App.Use(c.AuthMiddleware)
	c.App.Delete("/api/users", c.UserController.Logout)
	c.App.Patch("/api/users/_current", c.UserController.Update)
	c.App.Get("/api/users/_current", c.UserController.Current)

	c.App.Get("/api/contacts", c.ContactController.List)
	c.App.Post("/api/contacts", c.ContactController.Create)
	c.App.Put("/api/contacts/:contactId", c.ContactController.Update)
	c.App.Get("/api/contacts/:contactId", c.ContactController.Get)
	c.App.Delete("/api/contacts/:contactId", c.ContactController.Delete)

	c.App.Get("/api/contacts/:contactId/addresses", c.AddressController.List)
	c.App.Post("/api/contacts/:contactId/addresses", c.AddressController.Create)
	c.App.Put("/api/contacts/:contactId/addresses/:addressId", c.AddressController.Update)
	c.App.Get("/api/contacts/:contactId/addresses/:addressId", c.AddressController.Get)
	c.App.Delete("/api/contacts/:contactId/addresses/:addressId", c.AddressController.Delete)

	c.App.Get("/api/location", c.LocationController.Get)
	c.App.Get("/api/location/nearby", c.LocationController.ListNearby)
	c.App.Put("/api/location", c.LocationController.Update)

	c.App.Post("/api/sos", c.SOSController.Trigger)
	c.App.Post("/api/sos/classify", c.SOSController.Classify)
	c.App.Get("/api/sos/inbox", c.SOSController.ListInbox)
	c.App.Get("/api/sos/active", c.SOSController.ListActive)
	// Static paths must be registered before /api/sos/:alertId.
	c.App.Get("/api/sos/history", c.SOSController.ListHistory)
	c.App.Get("/api/sos/summary", c.SOSController.Summary)
	c.App.Get("/api/sos/:alertId", c.SOSController.Get)
	c.App.Get("/api/sos/:alertId/media", c.SOSController.ListMedia)
	c.App.Get("/api/sos/:alertId/media/:mediaId", c.SOSController.DownloadMedia)
	c.App.Post("/api/sos/:alertId/cancel", c.SOSController.Cancel)
	c.App.Post("/api/sos/:alertId/acknowledge", c.SOSController.Acknowledge)
	c.App.Post("/api/sos/:alertId/resolve", c.SOSController.Resolve)
	c.App.Post("/api/sos/:alertId/respond", c.SOSController.Respond)
	c.App.Get("/api/sos/:alertId/breadcrumbs", c.SOSController.ListBreadcrumbs)
	c.App.Post("/api/sos/:alertId/breadcrumbs", c.SOSController.CreateBreadcrumb)
	c.App.Post("/api/sos/:alertId/media", c.SOSController.UploadMedia)

	c.App.Get("/api/emergency-contacts", c.EmergencyContactController.List)
	c.App.Post("/api/emergency-contacts", c.EmergencyContactController.Create)

	c.App.Get("/api/push/vapid-public-key", c.PushController.VAPIDPublicKey)
	c.App.Get("/api/push/subscriptions", c.PushController.List)
	c.App.Post("/api/push/subscriptions", c.PushController.Subscribe)
	c.App.Delete("/api/push/subscriptions", c.PushController.Unsubscribe)
}
