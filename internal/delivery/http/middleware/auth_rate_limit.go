package middleware

import (
	"context"
	"math"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

// AuthRateLimiter is the per-IP limiter used by the guest auth routes.
// It is satisfied by *repository.RateLimitRepository.
type AuthRateLimiter interface {
	AllowAuth(ctx context.Context, ip string, limit int, window, lockout time.Duration) (bool, time.Duration, error)
}

// NewAuthRateLimit protects /api/users and /api/users/_login from
// password brute-force and registration spam. The limiter is failed open:
// a Redis outage must not lock users out of authentication.
func NewAuthRateLimit(limiter AuthRateLimiter, log *logrus.Logger, limit int, window, lockout time.Duration) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		if limiter == nil || limit <= 0 || window <= 0 || lockout <= 0 {
			return ctx.Next()
		}

		ip := ctx.IP()
		if ip == "" {
			ip = "unknown"
		}

		allowed, retryAfter, err := limiter.AllowAuth(ctx.UserContext(), ip, limit, window, lockout)
		if err != nil {
			if log != nil {
				log.WithError(err).Warn("auth rate limiter unavailable, proceeding")
			}
			return ctx.Next()
		}
		if allowed {
			return ctx.Next()
		}

		seconds := int(math.Ceil(retryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		ctx.Set("Retry-After", strconv.Itoa(seconds))
		return fiber.NewError(fiber.StatusTooManyRequests, "too many authentication attempts, try again later")
	}
}
