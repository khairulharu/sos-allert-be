package repository

import (
	"context"
	"errors"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PushSubscriptionRepository manages browser Web Push subscriptions.
type PushSubscriptionRepository struct {
	DB  *gorm.DB
	Log *logrus.Logger
}

func NewPushSubscriptionRepository(db *gorm.DB, log *logrus.Logger) *PushSubscriptionRepository {
	return &PushSubscriptionRepository{DB: db, Log: log}
}

// Upsert stores a subscription. An endpoint belongs to the browser that
// created it: re-subscribing the same endpoint refreshes that user's keys,
// while another user trying to claim it gets model.ErrPushEndpointOwned
// instead of taking it over. The ownership check and the write happen in
// one transaction with the endpoint row locked, so two racing subscribes
// cannot both win.
func (r *PushSubscriptionRepository) Upsert(ctx context.Context, sub *entity.PushSubscription) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing entity.PushSubscription
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("endpoint = ?", sub.Endpoint).First(&existing).Error
		switch {
		case err == nil:
			if existing.UserID != sub.UserID {
				return model.ErrPushEndpointOwned
			}
			// Same browser, same user: refresh the keys in place and
			// report the row that actually exists.
			sub.ID = existing.ID
			return tx.Model(&entity.PushSubscription{}).
				Where("id = ?", existing.ID).
				Updates(map[string]any{
					"p256dh":     sub.P256DH,
					"auth":       sub.Auth,
					"user_agent": sub.UserAgent,
					"updated_at": sub.UpdatedAt,
				}).Error
		case errors.Is(err, gorm.ErrRecordNotFound):
			if createErr := tx.Create(sub).Error; createErr != nil {
				// Lost a race with a concurrent subscribe for the same
				// endpoint: answer with the winner's owner.
				var owner entity.PushSubscription
				if findErr := tx.Where("endpoint = ?", sub.Endpoint).First(&owner).Error; findErr == nil {
					if owner.UserID != sub.UserID {
						return model.ErrPushEndpointOwned
					}
					return nil
				}
				return createErr
			}
			return nil
		default:
			return err
		}
	})
}

func (r *PushSubscriptionRepository) ListByUserID(ctx context.Context, userID string) ([]*entity.PushSubscription, error) {
	var subs []*entity.PushSubscription
	err := r.DB.WithContext(ctx).Where("user_id = ?", userID).Order("created_at ASC").Find(&subs).Error
	return subs, err
}

// DeleteByUserAndEndpoint removes one subscription the user owns.
func (r *PushSubscriptionRepository) DeleteByUserAndEndpoint(ctx context.Context, userID, endpoint string) error {
	return r.DB.WithContext(ctx).
		Where("user_id = ? AND endpoint = ?", userID, endpoint).
		Delete(&entity.PushSubscription{}).Error
}

// DeleteByEndpoint removes a subscription the push service reported as
// gone (404/410) during delivery; no owner is known at that point.
func (r *PushSubscriptionRepository) DeleteByEndpoint(ctx context.Context, endpoint string) error {
	return r.DB.WithContext(ctx).
		Where("endpoint = ?", endpoint).
		Delete(&entity.PushSubscription{}).Error
}
