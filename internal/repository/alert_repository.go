package repository

import (
	"context"
	"errors"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type AlertRepository struct {
	Repository[entity.Alert]
	Log *logrus.Logger
}

func NewAllertRepository(log *logrus.Logger) *AlertRepository {
	return &AlertRepository{Log: log}
}

func (a *AlertRepository) Create(ctx context.Context, db *gorm.DB, alert *entity.Alert) error {
	if err := db.WithContext(ctx).Create(alert).Error; err != nil {
		a.Log.WithError(err).Error("failed to create alert")
		return err
	}
	return nil
}

// GetByID loads one alert or returns model.ErrAlertNotFound.
func (a *AlertRepository) GetByID(ctx context.Context, db *gorm.DB, id string) (*entity.Alert, error) {
	alert := new(entity.Alert)
	if err := db.WithContext(ctx).Where("id = ?", id).First(alert).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, model.ErrAlertNotFound
		}
		return nil, err
	}
	return alert, nil
}

// ListActiveBySender returns the sender's TRIGGERED/ACKNOWLEDGED alerts,
// newest first.
func (a *AlertRepository) ListActiveBySender(ctx context.Context, db *gorm.DB, senderID string, limit int) ([]*entity.Alert, error) {
	var alerts []*entity.Alert
	if err := db.WithContext(ctx).
		Where("sender_id = ? AND status IN ?", senderID, []string{entity.AlertStatusTriggered, entity.AlertStatusAcknowledged}).
		Order("triggered_at DESC").Limit(limit).Find(&alerts).Error; err != nil {
		return nil, err
	}
	return alerts, nil
}

// Transition atomically moves an alert from one of fromStatuses to the
// fields in updates. It returns false (no error) when the alert is in a
// different state, so concurrent transitions cannot both succeed.
func (a *AlertRepository) Transition(ctx context.Context, db *gorm.DB, id string, fromStatuses []string, updates map[string]any) (bool, error) {
	res := db.WithContext(ctx).Model(&entity.Alert{}).
		Where("id = ? AND status IN ?", id, fromStatuses).Updates(updates)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// ExpireOlderThan moves stale TRIGGERED alerts to EXPIRED and returns
// how many were expired.
func (a *AlertRepository) ExpireOlderThan(ctx context.Context, db *gorm.DB, beforeMs int64, expiredAtMs int64) (int64, error) {
	res := db.WithContext(ctx).Model(&entity.Alert{}).
		Where("status = ? AND triggered_at < ?", entity.AlertStatusTriggered, beforeMs).
		Updates(map[string]any{"status": entity.AlertStatusExpired, "cancelled_at": nil, "resolved_at": nil})
	_ = expiredAtMs
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// CreateBreadcrumb appends one trajectory point.
func (a *AlertRepository) CreateBreadcrumb(ctx context.Context, db *gorm.DB, b *entity.AlertBreadcrumb) error {
	return db.WithContext(ctx).Create(b).Error
}

// ListBreadcrumbs returns the trail for an alert, oldest first.
func (a *AlertRepository) ListBreadcrumbs(ctx context.Context, db *gorm.DB, alertID string, limit int) ([]*entity.AlertBreadcrumb, error) {
	var out []*entity.AlertBreadcrumb
	if err := db.WithContext(ctx).Where("alert_id = ?", alertID).
		Order("recorded_at ASC").Limit(limit).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// LatestBreadcrumb returns the newest trail point, or nil.
func (a *AlertRepository) LatestBreadcrumb(ctx context.Context, db *gorm.DB, alertID string) (*entity.AlertBreadcrumb, error) {
	b := new(entity.AlertBreadcrumb)
	if err := db.WithContext(ctx).Where("alert_id = ?", alertID).
		Order("recorded_at DESC").First(b).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// CreateResponse records a responder answer.
func (a *AlertRepository) CreateResponse(ctx context.Context, db *gorm.DB, r *entity.AlertResponse) error {
	return db.WithContext(ctx).Create(r).Error
}

// CountResponders counts distinct responders for an alert.
func (a *AlertRepository) CountResponders(ctx context.Context, db *gorm.DB, alertID string) (int, error) {
	var count int64
	if err := db.WithContext(ctx).Model(&entity.AlertResponse{}).
		Where("alert_id = ?", alertID).Distinct("responder_id").Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

// IsResponder reports whether a user already responded to an alert.
func (a *AlertRepository) IsResponder(ctx context.Context, db *gorm.DB, alertID, userID string) (bool, error) {
	var count int64
	if err := db.WithContext(ctx).Model(&entity.AlertResponse{}).
		Where("alert_id = ? AND responder_id = ?", alertID, userID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// CreateMedia records an uploaded media file.
func (a *AlertRepository) CreateMedia(ctx context.Context, db *gorm.DB, m *entity.AlertMedia) error {
	return db.WithContext(ctx).Create(m).Error
}

// PurgeBreadcrumbs deletes trail points older than beforeMs for alerts
// that are already terminal (resolved/cancelled/expired). This is the
// 30-day ephemeral-retention rule from the README.
func (a *AlertRepository) PurgeBreadcrumbs(ctx context.Context, db *gorm.DB, beforeMs int64) (int64, error) {
	res := db.WithContext(ctx).Exec(
		"DELETE b FROM alert_breadcrumbs b JOIN alerts a ON a.id = b.alert_id "+
			"WHERE b.recorded_at < ? AND a.status IN ('RESOLVED','CANCELLED','EXPIRED')", beforeMs)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
