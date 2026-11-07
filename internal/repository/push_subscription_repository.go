package repository

import (
	"context"

	"golang-clean-architecture/internal/entity"

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

// Upsert stores a subscription, re-pointing an existing endpoint (same
// browser, possibly a different user) instead of duplicating it.
func (r *PushSubscriptionRepository) Upsert(ctx context.Context, sub *entity.PushSubscription) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "endpoint"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id", "p256dh", "auth", "user_agent", "updated_at"}),
	}).Create(sub).Error
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
