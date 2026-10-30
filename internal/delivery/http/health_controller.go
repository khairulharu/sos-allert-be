package http

import (
	"time"

	"github.com/gofiber/fiber/v2"
)

// HealthController serves the unauthenticated status probe used by
// the web MVP to show whether the backend is reachable and which
// triage mode is active. It performs no database or Redis check:
// startup already hard-fails when those are unreachable, so a
// responding process means the API is up. AIMode is "llm" when a
// provider is configured, otherwise "rules".
type HealthController struct {
	AIMode string
}

// NewHealthController builds the probe. An empty mode means
// rule-only triage.
func NewHealthController(aiMode string) *HealthController {
	if aiMode == "" {
		aiMode = "rules"
	}
	return &HealthController{AIMode: aiMode}
}

// Status returns the probe payload. It intentionally reveals no
// credentials, versions, or infrastructure details.
func (h *HealthController) Status(ctx *fiber.Ctx) error {
	return ctx.JSON(fiber.Map{
		"status":  "ok",
		"ai_mode": h.AIMode,
		"time":    time.Now().UnixMilli(),
	})
}
