package usecase

import (
	"context"
	"fmt"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// PushUseCase manages browser Web Push subscriptions and exposes the
// VAPID public key the browser needs to subscribe.
type PushUseCase struct {
	DB        *gorm.DB
	Log       *logrus.Logger
	Validate  *validator.Validate
	Subs      PushSubscriptionStore
	PublicKey string
	Enabled   bool
}

func NewPushUseCase(db *gorm.DB, log *logrus.Logger, validate *validator.Validate, subs PushSubscriptionStore, publicKey string, enabled bool) *PushUseCase {
	return &PushUseCase{DB: db, Log: log, Validate: validate, Subs: subs, PublicKey: publicKey, Enabled: enabled}
}

// VAPIDPublicKey returns the application server key browsers must use.
func (u *PushUseCase) VAPIDPublicKey() (string, error) {
	if !u.Enabled || u.PublicKey == "" {
		return "", fiber.ErrServiceUnavailable
	}
	return u.PublicKey, nil
}

// Subscribe stores (or re-points) a browser subscription for the user.
func (u *PushUseCase) Subscribe(ctx context.Context, req *model.PushSubscribeRequest) (*model.PushSubscriptionResponse, error) {
	if err := u.Validate.Struct(req); err != nil {
		return nil, fiber.ErrBadRequest
	}
	if u.Subs == nil {
		return nil, fmt.Errorf("push subscription store not configured")
	}
	now := time.Now().UnixMilli()
	sub := &entity.PushSubscription{
		ID:        uuid.NewString(),
		UserID:    req.UserID,
		Endpoint:  req.Endpoint,
		P256DH:    req.P256DH,
		Auth:      req.Auth,
		UserAgent: req.UserAgent,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := u.Subs.Upsert(ctx, sub); err != nil {
		return nil, err
	}
	return &model.PushSubscriptionResponse{
		ID: sub.ID, Endpoint: sub.Endpoint, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// Unsubscribe removes the user's subscription for one endpoint. Unknown
// endpoints are a no-op so the call is idempotent.
func (u *PushUseCase) Unsubscribe(ctx context.Context, req *model.PushUnsubscribeRequest) error {
	if err := u.Validate.Struct(req); err != nil {
		return fiber.ErrBadRequest
	}
	if u.Subs == nil {
		return fmt.Errorf("push subscription store not configured")
	}
	return u.Subs.DeleteByUserAndEndpoint(ctx, req.UserID, req.Endpoint)
}

// List returns the user's subscriptions.
func (u *PushUseCase) List(ctx context.Context, userID string) ([]model.PushSubscriptionResponse, error) {
	if u.Subs == nil {
		return nil, fmt.Errorf("push subscription store not configured")
	}
	subs, err := u.Subs.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]model.PushSubscriptionResponse, 0, len(subs))
	for _, s := range subs {
		out = append(out, model.PushSubscriptionResponse{ID: s.ID, Endpoint: s.Endpoint, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt})
	}
	return out, nil
}
