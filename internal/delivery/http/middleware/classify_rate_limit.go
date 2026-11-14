package middleware

import (
	"context"
	"math"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

// ClassifyRateLimiter is the per-user limiter for the paid classification
// route. It is satisfied by *repository.RateLimitRepository.
type ClassifyRateLimiter interface {
	AllowClassify(ctx context.Context, userID string, limit int, window time.Duration) (bool, error)
}

// NewClassifyRateLimit caps how often one user may call the speech-to-text
// plus LLM classification route, which costs money per request. Unlike the
// auth limiter it fails closed: when the store is unavailable the call is
// refused with 503 so an outage cannot become an unmetered bill.
func NewClassifyRateLimit(limiter ClassifyRateLimiter, log *logrus.Logger, limit int, window time.Duration) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		if limiter == nil || limit <= 0 || window <= 0 {
			return ctx.Next()
		}

		auth := GetUser(ctx)
		allowed, err := limiter.AllowClassify(ctx.UserContext(), auth.ID, limit, window)
		if err != nil {
			if log != nil {
				log.WithError(err).Warn("classify rate limiter unavailable, rejecting the paid call")
			}
			return fiber.NewError(fiber.StatusServiceUnavailable, "classification is temporarily unavailable")
		}
		if allowed {
			return ctx.Next()
		}

		// The sliding window keeps no per-caller deadline, so report the
		// window length as the upper bound.
		seconds := int(math.Ceil(window.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		ctx.Set("Retry-After", strconv.Itoa(seconds))
		return fiber.NewError(fiber.StatusTooManyRequests, "classification limit reached, try again later")
	}
}
