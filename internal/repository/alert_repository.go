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

// CountActive counts live incidents (TRIGGERED/ACKNOWLEDGED), the source
// of truth for the active_sos_count gauge.
func (a *AlertRepository) CountActive(ctx context.Context, db *gorm.DB) (int64, error) {
	var n int64
	if err := db.WithContext(ctx).Model(&entity.Alert{}).
		Where("status IN ?", []string{entity.AlertStatusTriggered, entity.AlertStatusAcknowledged}).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
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

// ListResponses returns the responders of an incident, oldest first,
// backing the responder list on the alert detail view.
func (a *AlertRepository) ListResponses(ctx context.Context, db *gorm.DB, alertID string) ([]*entity.AlertResponse, error) {
	var out []*entity.AlertResponse
	if err := db.WithContext(ctx).Where("alert_id = ?", alertID).
		Order("responded_at ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
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

// alertStatusesFor maps the API status filter to statuses. Empty means
// "all" and returns nil; "active"/"closed" are the group shorthands the
// frontend uses for its chips.
func alertStatusesFor(filter string) []string {
	switch filter {
	case "":
		return nil
	case "active":
		return []string{entity.AlertStatusTriggered, entity.AlertStatusAcknowledged}
	case "closed":
		return []string{entity.AlertStatusResolved, entity.AlertStatusCancelled, entity.AlertStatusExpired}
	default:
		return []string{filter}
	}
}

// applyAlertHistoryFilter adds the status/window predicates shared by
// both history queries (and their counts).
func applyAlertHistoryFilter(tx *gorm.DB, req *model.SearchAlertHistoryRequest) *gorm.DB {
	if statuses := alertStatusesFor(req.Status); len(statuses) > 0 {
		tx = tx.Where("alerts.status IN ?", statuses)
	}
	if req.FromMs != nil {
		tx = tx.Where("alerts.triggered_at >= ?", *req.FromMs)
	}
	if req.ToMs != nil {
		tx = tx.Where("alerts.triggered_at <= ?", *req.ToMs)
	}
	return tx
}

func senderHistoryQuery(ctx context.Context, db *gorm.DB, senderID string) *gorm.DB {
	return db.WithContext(ctx).Model(&entity.Alert{}).Where("alerts.sender_id = ?", senderID)
}

func recipientHistoryQuery(ctx context.Context, db *gorm.DB, userID string) *gorm.DB {
	return db.WithContext(ctx).Model(&entity.Alert{}).
		Joins("JOIN alert_recipients r ON r.alert_id = alerts.id").
		Where("r.user_id = ?", userID)
}

// ListHistoryBySender returns one page of the caller's sent incidents
// (all statuses) plus the total row count, newest first.
func (a *AlertRepository) ListHistoryBySender(ctx context.Context, db *gorm.DB, req *model.SearchAlertHistoryRequest) ([]*entity.Alert, int64, error) {
	var out []*entity.Alert
	if err := applyAlertHistoryFilter(senderHistoryQuery(ctx, db, req.UserID), req).
		Order("alerts.triggered_at DESC, alerts.id DESC").
		Offset((req.Page - 1) * req.Size).Limit(req.Size).
		Find(&out).Error; err != nil {
		return nil, 0, err
	}

	var total int64
	if err := applyAlertHistoryFilter(senderHistoryQuery(ctx, db, req.UserID), req).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListHistoryForRecipient returns one page of the incidents the caller
// was notified for, plus the total. The sender never enters
// alert_recipients, so a user's own alerts are never listed here.
func (a *AlertRepository) ListHistoryForRecipient(ctx context.Context, db *gorm.DB, req *model.SearchAlertHistoryRequest) ([]*entity.Alert, int64, error) {
	var out []*entity.Alert
	if err := applyAlertHistoryFilter(recipientHistoryQuery(ctx, db, req.UserID), req).
		Select("alerts.*").
		Order("alerts.triggered_at DESC, alerts.id DESC").
		Offset((req.Page - 1) * req.Size).Limit(req.Size).
		Find(&out).Error; err != nil {
		return nil, 0, err
	}

	var total int64
	if err := applyAlertHistoryFilter(recipientHistoryQuery(ctx, db, req.UserID), req).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// statusCountRows is the scan target for the GROUP BY status queries.
type statusCountRow struct {
	Status string
	Total  int64
}

func statusCountMap(rows []statusCountRow) map[string]int64 {
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Status] = row.Total
	}
	return out
}

// CountBySenderStatus groups the caller's sent alerts by status.
func (a *AlertRepository) CountBySenderStatus(ctx context.Context, db *gorm.DB, senderID string) (map[string]int64, error) {
	var rows []statusCountRow
	if err := db.WithContext(ctx).Model(&entity.Alert{}).
		Select("status, COUNT(*) AS total").
		Where("sender_id = ?", senderID).
		Group("status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	return statusCountMap(rows), nil
}

// CountByRecipientStatus groups the alerts the caller was notified for
// by status (all of them, active or closed).
func (a *AlertRepository) CountByRecipientStatus(ctx context.Context, db *gorm.DB, userID string) (map[string]int64, error) {
	var rows []statusCountRow
	if err := db.WithContext(ctx).Model(&entity.Alert{}).
		Select("alerts.status AS status, COUNT(*) AS total").
		Joins("JOIN alert_recipients r ON r.alert_id = alerts.id").
		Where("r.user_id = ?", userID).
		Group("alerts.status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	return statusCountMap(rows), nil
}

// CountResponded reports how many distinct incidents the caller answered
// (total) and how many of those ended RESOLVED. COUNT(DISTINCT ...) is
// required: alert_responses has no unique (alert_id, responder_id).
func (a *AlertRepository) CountResponded(ctx context.Context, db *gorm.DB, userID string) (total, resolved int64, err error) {
	if err = db.WithContext(ctx).Model(&entity.AlertResponse{}).
		Where("responder_id = ?", userID).
		Select("COUNT(DISTINCT alert_id)").Scan(&total).Error; err != nil {
		return 0, 0, err
	}
	if err = db.WithContext(ctx).Table("alert_responses r").
		Joins("JOIN alerts a ON a.id = r.alert_id").
		Where("r.responder_id = ? AND a.status = ?", userID, entity.AlertStatusResolved).
		Select("COUNT(DISTINCT r.alert_id)").Scan(&resolved).Error; err != nil {
		return 0, 0, err
	}
	return total, resolved, nil
}

// CountRespondersByAlertIDs batches the responder count for a page of
// alerts, so a history page costs one query instead of one per row.
func (a *AlertRepository) CountRespondersByAlertIDs(ctx context.Context, db *gorm.DB, alertIDs []string) (map[string]int, error) {
	out := make(map[string]int, len(alertIDs))
	if len(alertIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		AlertID string
		Total   int64
	}
	if err := db.WithContext(ctx).Model(&entity.AlertResponse{}).
		Select("alert_id, COUNT(DISTINCT responder_id) AS total").
		Where("alert_id IN ?", alertIDs).
		Group("alert_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.AlertID] = int(row.Total)
	}
	return out, nil
}

// LatestBreadcrumbsByAlertIDs batches the newest trail point for a page
// of alerts (one derived-table query, MySQL 5.7+). Ties on recorded_at
// keep the first row.
func (a *AlertRepository) LatestBreadcrumbsByAlertIDs(ctx context.Context, db *gorm.DB, alertIDs []string) (map[string]*entity.AlertBreadcrumb, error) {
	out := make(map[string]*entity.AlertBreadcrumb, len(alertIDs))
	if len(alertIDs) == 0 {
		return out, nil
	}
	var rows []*entity.AlertBreadcrumb
	if err := db.WithContext(ctx).Raw(`
		SELECT b.* FROM alert_breadcrumbs b
		JOIN (SELECT alert_id, MAX(recorded_at) AS max_recorded
		      FROM alert_breadcrumbs
		      WHERE alert_id IN ?
		      GROUP BY alert_id) m
		  ON m.alert_id = b.alert_id AND m.max_recorded = b.recorded_at
		WHERE b.alert_id IN ?`, alertIDs, alertIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, crumb := range rows {
		if _, ok := out[crumb.AlertID]; !ok {
			out[crumb.AlertID] = crumb
		}
	}
	return out, nil
}

// ListRespondedAlertIDs reports which of alertIDs the user answered,
// so a history page can apply the existing masking rule in one query.
func (a *AlertRepository) ListRespondedAlertIDs(ctx context.Context, db *gorm.DB, userID string, alertIDs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(alertIDs))
	if len(alertIDs) == 0 {
		return out, nil
	}
	var ids []string
	if err := db.WithContext(ctx).Model(&entity.AlertResponse{}).
		Where("responder_id = ? AND alert_id IN ?", userID, alertIDs).
		Distinct().Pluck("alert_id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
