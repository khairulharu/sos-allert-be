package usecase

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"golang-clean-architecture/internal/model"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

type fakeSessionStore struct {
	userID    string
	getErr    error
	deleteErr error

	createCalls int
	gotUserID   string
	gotToken    string
	gotTTL      time.Duration
	lastDeleted string
}

func (f *fakeSessionStore) Create(ctx context.Context, userID, token string, ttl time.Duration) error {
	f.createCalls++
	f.gotUserID = userID
	f.gotToken = token
	f.gotTTL = ttl
	return nil
}

func (f *fakeSessionStore) Get(ctx context.Context, token string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	if token == "" {
		return "", nil
	}
	return f.userID, nil
}

func (f *fakeSessionStore) DeleteByUser(ctx context.Context, userID string) error {
	f.lastDeleted = userID
	return f.deleteErr
}

func newUserUseCaseForTest(sessions SessionStore) *UserUseCase {
	log := logrus.New()
	log.SetOutput(io.Discard)
	return NewUserUseCase(nil, log, validator.New(), nil, sessions, 168*time.Hour, nil)
}

func TestVerifyReturnsUserFromSession(t *testing.T) {
	sessions := &fakeSessionStore{userID: "user-1"}
	uc := newUserUseCaseForTest(sessions)

	auth, err := uc.Verify(context.Background(), &model.VerifyUserRequest{Token: "session-token"})
	if err != nil {
		t.Fatalf("Verify error = %v", err)
	}
	if auth.ID != "user-1" {
		t.Fatalf("auth.ID = %q, want user-1", auth.ID)
	}
}

func TestVerifyRejectsUnknownSession(t *testing.T) {
	sessions := &fakeSessionStore{userID: ""}
	uc := newUserUseCaseForTest(sessions)

	_, err := uc.Verify(context.Background(), &model.VerifyUserRequest{Token: "expired-token"})
	if !errors.Is(err, fiber.ErrUnauthorized) {
		t.Fatalf("Verify error = %v, want 401", err)
	}
}

func TestVerifyRejectsEmptyToken(t *testing.T) {
	uc := newUserUseCaseForTest(&fakeSessionStore{})

	_, err := uc.Verify(context.Background(), &model.VerifyUserRequest{Token: ""})
	if !errors.Is(err, fiber.ErrUnauthorized) {
		t.Fatalf("Verify error = %v, want 401", err)
	}
}

func TestVerifyFailsClosedOnStoreError(t *testing.T) {
	sessions := &fakeSessionStore{getErr: errors.New("redis down")}
	uc := newUserUseCaseForTest(sessions)

	_, err := uc.Verify(context.Background(), &model.VerifyUserRequest{Token: "session-token"})
	if !errors.Is(err, fiber.ErrServiceUnavailable) {
		t.Fatalf("Verify error = %v, want 503", err)
	}
}

func TestLogoutDeletesSession(t *testing.T) {
	sessions := &fakeSessionStore{}
	uc := newUserUseCaseForTest(sessions)

	ok, err := uc.Logout(context.Background(), &model.LogoutUserRequest{ID: "user-1"})
	if err != nil || !ok {
		t.Fatalf("Logout = %v, %v; want true, nil", ok, err)
	}
	if sessions.lastDeleted != "user-1" {
		t.Fatalf("deleted user = %q, want user-1", sessions.lastDeleted)
	}
}

func TestLogoutFailsWhenStoreErrors(t *testing.T) {
	sessions := &fakeSessionStore{deleteErr: errors.New("redis down")}
	uc := newUserUseCaseForTest(sessions)

	_, err := uc.Logout(context.Background(), &model.LogoutUserRequest{ID: "user-1"})
	if !errors.Is(err, fiber.ErrServiceUnavailable) {
		t.Fatalf("Logout error = %v, want 503", err)
	}
}
