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

// Emergency-contact validation errors are client errors with a usable
// message, never an opaque 500.
func TestMapSOSErrorContactValidationIsUnprocessable(t *testing.T) {
	for _, err := range []error{
		model.ErrInvalidPhoneNumber,
		model.ErrTooManyEmergencyContacts,
		model.ErrDuplicateEmergencyContact,
	} {
		mapped := mapSOSError(err)

		var fiberErr *fiber.Error
		if !errors.As(mapped, &fiberErr) {
			t.Fatalf("mapSOSError(%v) = %v, want a *fiber.Error", err, mapped)
		}
		if fiberErr.Code != fiber.StatusUnprocessableEntity {
			t.Fatalf("mapSOSError(%v) status = %d, want 422", err, fiberErr.Code)
		}
		if fiberErr.Message == "" {
			t.Fatalf("mapSOSError(%v) message is empty", err)
		}
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

// A repeated responder answer is a client conflict (409) with a usable
// message, never an opaque 500 (#42).
func TestMapSOSErrorDuplicateResponseIsConflict(t *testing.T) {
	mapped := mapSOSError(fmt.Errorf("respond failed: %w", model.ErrDuplicateResponse))

	var fiberErr *fiber.Error
	if !errors.As(mapped, &fiberErr) {
		t.Fatalf("mapSOSError(ErrDuplicateResponse) = %v, want a *fiber.Error", mapped)
	}
	if fiberErr.Code != fiber.StatusConflict {
		t.Fatalf("status = %d, want 409", fiberErr.Code)
	}
	if fiberErr.Message != "responder has already responded to this alert" {
		t.Fatalf("message = %q, want the duplicate-responder message", fiberErr.Message)
	}
}
