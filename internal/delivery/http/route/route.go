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
	SOSTrackController         *http.SOSTrackController
	MetricsHandler             fiber.Handler
	AuthMiddleware             fiber.Handler
}

func (c *RouteConfig) Setup() {
	c.SetupGuestRoute()
	c.SetupAuthRoute()
}

func (c *RouteConfig) SetupGuestRoute() {
	c.App.Post("/api/users", c.UserController.Register)
	c.App.Post("/api/users/_login", c.UserController.Login)
	if c.MetricsHandler != nil {
		c.App.Get("/metrics", c.MetricsHandler)
	}
	// WebSocket tracking authenticates with ?token=, so it is
	// registered before the header-based auth middleware.
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

	c.App.Put("/api/location", c.LocationController.Update)

	c.App.Post("/api/sos", c.SOSController.Trigger)
	c.App.Post("/api/ai/triage", c.SOSController.PreviewTriage)
	c.App.Get("/api/sos/active", c.SOSController.ListActive)
	c.App.Get("/api/sos/:alertId", c.SOSController.Get)
	c.App.Post("/api/sos/:alertId/cancel", c.SOSController.Cancel)
	c.App.Post("/api/sos/:alertId/acknowledge", c.SOSController.Acknowledge)
	c.App.Post("/api/sos/:alertId/resolve", c.SOSController.Resolve)
	c.App.Post("/api/sos/:alertId/respond", c.SOSController.Respond)
	c.App.Get("/api/sos/:alertId/breadcrumbs", c.SOSController.ListBreadcrumbs)
	c.App.Post("/api/sos/:alertId/breadcrumbs", c.SOSController.CreateBreadcrumb)
	c.App.Post("/api/sos/:alertId/media", c.SOSController.UploadMedia)

	c.App.Get("/api/emergency-contacts", c.EmergencyContactController.List)
	c.App.Post("/api/emergency-contacts", c.EmergencyContactController.Create)
}
