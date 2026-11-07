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

// UpdateTriage refreshes the classification after post-trigger audio
// transcription. Empty fields are left untouched.
func (a *AlertRepository) UpdateTriage(ctx context.Context, db *gorm.DB, alertID, emergencyType string, severity float64, summary string) error {
	updates := map[string]any{}
	if emergencyType != "" {
		updates["emergency_type"] = emergencyType
	}
	if severity > 0 {
		updates["severity"] = severity
	}
	if summary != "" {
		updates["summary"] = summary
	}
	if len(updates) == 0 {
		return nil
	}
	return db.WithContext(ctx).Model(&entity.Alert{}).Where("id = ?", alertID).Updates(updates).Error
}

// ListMedia returns an incident's uploads, oldest first.
func (a *AlertRepository) ListMedia(ctx context.Context, db *gorm.DB, alertID string) ([]*entity.AlertMedia, error) {
	var out []*entity.AlertMedia
	if err := db.WithContext(ctx).Where("alert_id = ?", alertID).
		Order("uploaded_at ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// GetMedia loads one upload, or model.ErrMediaNotFound.
func (a *AlertRepository) GetMedia(ctx context.Context, db *gorm.DB, alertID, mediaID string) (*entity.AlertMedia, error) {
	m := new(entity.AlertMedia)
	if err := db.WithContext(ctx).
		Where("id = ? AND alert_id = ?", mediaID, alertID).First(m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, model.ErrMediaNotFound
		}
		return nil, err
	}
	return m, nil
}

// CreateRecipients records who was notified for an incident. Duplicate
// inserts are ignored so a retried trigger stays idempotent.
func (a *AlertRepository) CreateRecipients(ctx context.Context, db *gorm.DB, alertID string, userIDs []string, notifiedAt int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	rows := make([]*entity.AlertRecipient, 0, len(userIDs))
	for _, userID := range userIDs {
		rows = append(rows, &entity.AlertRecipient{AlertID: alertID, UserID: userID, NotifiedAt: notifiedAt})
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// ListRecipients returns the user ids already notified for an incident.
// Used to widen the fan-out after a post-trigger severity escalation
// without re-notifying the original recipients.
func (a *AlertRepository) ListRecipients(ctx context.Context, db *gorm.DB, alertID string) ([]string, error) {
	var out []string
	if err := db.WithContext(ctx).Model(&entity.AlertRecipient{}).
		Where("alert_id = ?", alertID).Pluck("user_id", &out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// ListInbox returns the active incidents a user was notified for,
// newest first (the responder inbox behind GET /api/sos/inbox).
func (a *AlertRepository) ListInbox(ctx context.Context, db *gorm.DB, userID string, limit int) ([]*entity.Alert, error) {
	var out []*entity.Alert
	if err := db.WithContext(ctx).Model(&entity.Alert{}).
		Select("alerts.*").
		Joins("JOIN alert_recipients r ON r.alert_id = alerts.id").
		Where("r.user_id = ? AND alerts.status IN ?", userID, []string{entity.AlertStatusTriggered, entity.AlertStatusAcknowledged}).
		Order("alerts.triggered_at DESC").Limit(limit).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
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
