package middleware

import (
	"context"
	"errors"

	"golang-clean-architecture/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

// TokenVerifier resolves a raw session token to the authenticated user.
// Satisfied by *usecase.UserUseCase. Keeping it an interface lets the
// middleware be tested without Redis or MySQL.
type TokenVerifier interface {
	Verify(ctx context.Context, request *model.VerifyUserRequest) (*model.Auth, error)
}

func NewAuth(verifier TokenVerifier, log *logrus.Logger) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		// Never log the token itself: logs may be shipped off-host.
		request := &model.VerifyUserRequest{Token: ctx.Get("Authorization", "NOT_FOUND")}

		auth, err := verifier.Verify(ctx.UserContext(), request)
		if err != nil {
			if log != nil {
				log.Warnf("Failed find user by token : %+v", err)
			}
			// A session-store outage is not an invalid token: tell the client
			// to retry instead of discarding its session.
			if errors.Is(err, fiber.ErrServiceUnavailable) {
				return fiber.ErrServiceUnavailable
			}
			return fiber.ErrUnauthorized
		}

		if log != nil {
			log.Debugf("User : %+v", auth.ID)
		}
		ctx.Locals("auth", auth)
		return ctx.Next()
	}
}

func GetUser(ctx *fiber.Ctx) *model.Auth {
	auth, ok := ctx.Locals("auth").(*model.Auth)
	if !ok {
		// Only reachable when a route is mounted without AuthMiddleware.
		panic(fiber.ErrInternalServerError)
	}
	return auth
}
