package config

import (
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

	// CORS: allow the Vite dev origin and any configured origin so the
	// browser app (localhost:5173) can talk to the API (localhost:3000).
	// The demo app sends a raw Authorization header + X-Idempotency-Key,
	// so those must be exposed/allowed. Customize via config if needed.
	app.Use(cors.New(cors.Config{
		AllowOrigins:     "*",
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
