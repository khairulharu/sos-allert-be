package config

import (
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/spf13/viper"
)

func NewFiber(config *viper.Viper) *fiber.App {
	proxyHeader := config.GetString("web.proxy_header")
	trustedProxies := config.GetStringSlice("web.trusted_proxies")

	fiberConfig := fiber.Config{
		AppName:      config.GetString("app.name"),
		ErrorHandler: NewErrorHandler(),
		Prefork:      config.GetBool("web.prefork"),
		// fasthttp reads the body (multipart included) before the handler,
		// so a route-level limit cannot raise this: it must cover the
		// largest incident upload. Keep in sync with usecase.maxMediaBytes.
		BodyLimit: 50 << 20,
		// Set web.proxy_header (e.g. "X-Forwarded-For") only when the
		// app sits behind a trusted reverse proxy; ctx.IP() then uses
		// the client address for the auth rate limiter.
		ProxyHeader: proxyHeader,
	}

	// Hardening: when a proxy header is in use, trust it only from the
	// configured proxy addresses (web.trusted_proxies). Without that
	// list a client that reaches the app directly can spoof
	// X-Forwarded-For and bypass the per-IP auth limiter. The check is
	// opt-in so existing configs that only set proxy_header keep
	// working; we warn instead of silently changing behavior.
	if proxyHeader != "" {
		fiberConfig.EnableIPValidation = true
		if len(trustedProxies) > 0 {
			fiberConfig.EnableTrustedProxyCheck = true
			fiberConfig.TrustedProxies = trustedProxies
		} else {
			log.Printf("WARNING: web.proxy_header is set but web.trusted_proxies is empty: " +
				"X-Forwarded-For is honored unconditionally and is spoofable if the app " +
				"is reachable directly. Set web.trusted_proxies (e.g. " +
				"[\"127.0.0.1\",\"::1\"] for a local cloudflared/nginx) to harden it.")
		}
	}

	app := fiber.New(fiberConfig)

	// CORS: production origin + Vite dev origin only. The browser app
	// sends a raw Authorization header + X-Idempotency-Key, so those
	// must be exposed/allowed.
	app.Use(cors.New(cors.Config{
		AllowOrigins:     "https://allert.my.id, http://localhost:5173",
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders:     "Origin,Content-Type,Accept,Authorization,X-Idempotency-Key",
		ExposeHeaders:    "Content-Disposition",
		AllowCredentials: false,
	}))

	return app
}

// NewErrorHandler renders errors in the documented WebResponse shape.
func NewErrorHandler() fiber.ErrorHandler {
	return func(ctx *fiber.Ctx, err error) error {
		code := fiber.StatusInternalServerError
		if e, ok := err.(*fiber.Error); ok {
			code = e.Code
		}

		return ctx.Status(code).JSON(fiber.Map{
			"errors": err.Error(),
		})
	}
}
