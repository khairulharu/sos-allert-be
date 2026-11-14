package middleware

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang-clean-architecture/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

type fakeTokenVerifier struct {
	auth       *model.Auth
	err        error
	gotToken   string
	verifyCall int
}

func (f *fakeTokenVerifier) Verify(ctx context.Context, request *model.VerifyUserRequest) (*model.Auth, error) {
	f.verifyCall++
	f.gotToken = request.Token
	return f.auth, f.err
}

func newAuthTestApp(verifier TokenVerifier, log *logrus.Logger) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return ctx.Status(code).JSON(fiber.Map{"errors": err.Error()})
		},
	})
	app.Get("/protected", NewAuth(verifier, log), func(ctx *fiber.Ctx) error {
		return ctx.JSON(fiber.Map{"user_id": GetUser(ctx).ID})
	})
	return app
}

func authHeaderRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	return req
}

func discardLogger() *logrus.Logger {
	log := logrus.New()
	log.SetOutput(io.Discard)
	return log
}

func TestAuthMiddlewareAllowsValidToken(t *testing.T) {
	verifier := &fakeTokenVerifier{auth: &model.Auth{ID: "user-1"}}
	app := newAuthTestApp(verifier, discardLogger())

	resp, err := app.Test(authHeaderRequest("session-token"))
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if verifier.gotToken != "session-token" {
		t.Fatalf("token passed to verifier = %q", verifier.gotToken)
	}
}

func TestAuthMiddlewareRejectsUnknownToken(t *testing.T) {
	verifier := &fakeTokenVerifier{err: fiber.ErrUnauthorized}
	app := newAuthTestApp(verifier, discardLogger())

	resp, err := app.Test(authHeaderRequest("expired-token"))
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuthMiddlewareReturns503WhenStoreIsDown(t *testing.T) {
	verifier := &fakeTokenVerifier{err: fiber.ErrServiceUnavailable}
	app := newAuthTestApp(verifier, discardLogger())

	resp, err := app.Test(authHeaderRequest("session-token"))
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestAuthMiddlewareNeverLogsToken(t *testing.T) {
	var buf bytes.Buffer
	log := logrus.New()
	log.SetOutput(&buf)
	log.SetLevel(logrus.DebugLevel)

	verifier := &fakeTokenVerifier{auth: &model.Auth{ID: "user-1"}}
	app := newAuthTestApp(verifier, log)

	resp, err := app.Test(authHeaderRequest("super-secret-session-token"))
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	resp.Body.Close()

	if strings.Contains(buf.String(), "super-secret-session-token") {
		t.Fatalf("token leaked into logs: %q", buf.String())
	}
}
