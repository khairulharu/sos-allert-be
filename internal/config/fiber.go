package config

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/spf13/viper"
)

func NewFiber(config *viper.Viper) *fiber.App {
	var app = fiber.New(fiber.Config{
		AppName:      config.GetString("app.name"),
		ErrorHandler: NewErrorHandler(),
		Prefork:      config.GetBool("web.prefork"),
	})

	// CORS for the SvelteKit web MVP, which runs on a different
	// origin (dev :5173 / preview :4173) and calls the API with the
	// raw Authorization token header. Origins are configurable via
	// web.cors_origins (comma-separated); the default allows the
	// local dev/preview origins plus the API origin itself. Auth
	// uses a header, not cookies, so no credentials are needed.
	origins := config.GetString("web.cors_origins")
	if origins == "" {
		origins = "http://localhost:5173,http://localhost:4173,http://127.0.0.1:5173,http://127.0.0.1:4173"
	}
	origins = strings.TrimSpace(origins)
	app.Use(cors.New(cors.Config{
		AllowOrigins: origins,
		AllowHeaders: "Origin, Content-Type, Accept, Authorization, X-Idempotency-Key",
		AllowMethods: "GET, POST, PUT, PATCH, DELETE, OPTIONS",
	}))

	return app
}

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
