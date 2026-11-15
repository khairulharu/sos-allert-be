package http

import (
	"errors"
	"fmt"
	"testing"

	"golang-clean-architecture/internal/model"

	"github.com/gofiber/fiber/v2"
)

// A wrong safe PIN is an authorization failure, not a session failure: 401
// must stay reserved for an expired/unknown token, otherwise the client
// logs the user out on a mistyped PIN.
func TestMapSOSErrorInvalidPINIsNotUnauthorized(t *testing.T) {
	mapped := mapSOSError(fmt.Errorf("cancel failed: %w", model.ErrInvalidPIN))

	var fiberErr *fiber.Error
	if !errors.As(mapped, &fiberErr) {
		t.Fatalf("mapSOSError(ErrInvalidPIN) = %v, want a *fiber.Error", mapped)
	}
	if fiberErr.Code == fiber.StatusUnauthorized {
		t.Fatal("invalid PIN must not be 401 (session-failure semantics)")
	}
	if fiberErr.Code != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", fiberErr.Code)
	}
	if fiberErr.Message != "invalid safe PIN" {
		t.Fatalf("message = %q, want %q", fiberErr.Message, "invalid safe PIN")
	}
}

// Only a real session failure stays 401.
func TestMapSOSErrorSessionFailureStaysUnauthorized(t *testing.T) {
	mapped := mapSOSError(fiber.ErrUnauthorized)

	var fiberErr *fiber.Error
	if !errors.As(mapped, &fiberErr) || fiberErr.Code != fiber.StatusUnauthorized {
		t.Fatalf("mapSOSError(fiber.ErrUnauthorized) = %v, want 401", mapped)
	}
}

// Read paths hide a foreign alert behind the same 404 as an unknown id.
func TestMapSOSErrorAlertNotFoundIsNotFound(t *testing.T) {
	mapped := mapSOSError(fmt.Errorf("wrapped: %w", model.ErrAlertNotFound))

	var fiberErr *fiber.Error
	if !errors.As(mapped, &fiberErr) || fiberErr.Code != fiber.StatusNotFound {
		t.Fatalf("mapSOSError(ErrAlertNotFound) = %v, want 404", mapped)
	}
}
