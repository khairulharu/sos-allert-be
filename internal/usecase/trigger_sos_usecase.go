package usecase

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const staleThreshold = 2 * 60 * 1000 // 2 minutes in milliseconds

// LocationRepository is the subset of location storage used by TriggerSOS.
// It is satisfied by *repository.LocationCacheRepository (Redis GEO).
type LocationRepository interface {
	Get(ctx context.Context, userId string) (*entity.LocationCache, error)
	Save(ctx context.Context, loc *entity.LocationCache) error
	GeoSearch(ctx context.Context, longitude float64, latitude float64, radiusMeters float64) ([]string, error)
	GeoSearchWithDistance(ctx context.Context, longitude float64, latitude float64, radiusMeters float64, count int) ([]entity.GeoLocation, error)
	GetLastSeen(ctx context.Context, userId string) (int64, error)
	GetLastSeenBatch(ctx context.Context, userIds []string) (map[string]int64, error)
}

// AlertStore is the full SOS persistence surface.
// Satisfied by *repository.AlertRepository.
type AlertStore interface {
	Create(ctx context.Context, db *gorm.DB, alert *entity.Alert) error
	GetByID(ctx context.Context, db *gorm.DB, id string) (*entity.Alert, error)
	ListActiveBySender(ctx context.Context, db *gorm.DB, senderID string, limit int) ([]*entity.Alert, error)
	Transition(ctx context.Context, db *gorm.DB, id string, fromStatuses []string, updates map[string]any) (bool, error)
	ExpireOlderThan(ctx context.Context, db *gorm.DB, beforeMs int64, expiredAtMs int64) (int64, error)
	CreateBreadcrumb(ctx context.Context, db *gorm.DB, b *entity.AlertBreadcrumb) error
	ListBreadcrumbs(ctx context.Context, db *gorm.DB, alertID string, limit int) ([]*entity.AlertBreadcrumb, error)
	LatestBreadcrumb(ctx context.Context, db *gorm.DB, alertID string) (*entity.AlertBreadcrumb, error)
	CreateResponse(ctx context.Context, db *gorm.DB, r *entity.AlertResponse) error
	CountResponders(ctx context.Context, db *gorm.DB, alertID string) (int, error)
	IsResponder(ctx context.Context, db *gorm.DB, alertID, userID string) (bool, error)
	CreateMedia(ctx context.Context, db *gorm.DB, m *entity.AlertMedia) error
	ListMedia(ctx context.Context, db *gorm.DB, alertID string) ([]*entity.AlertMedia, error)
	GetMedia(ctx context.Context, db *gorm.DB, alertID, mediaID string) (*entity.AlertMedia, error)
	UpdateTriage(ctx context.Context, db *gorm.DB, alertID, emergencyType string, severity float64, summary string) error
	CreateRecipients(ctx context.Context, db *gorm.DB, alertID string, userIDs []string, notifiedAt int64) error
	ListRecipients(ctx context.Context, db *gorm.DB, alertID string) ([]string, error)
	ListInbox(ctx context.Context, db *gorm.DB, userID string, limit int) ([]*entity.Alert, error)
	PurgeBreadcrumbs(ctx context.Context, db *gorm.DB, beforeMs int64) (int64, error)
}

// AlertSaver is the legacy create-only subset, kept for compatibility.
type AlertSaver interface {
	Create(ctx context.Context, db *gorm.DB, alert *entity.Alert) error
}

// Notifier sends SOS notifications to recipients.
// Satisfied by *AlertNotifier and *AlertDispatcher.
type Notifier interface {
	Notify(ctx context.Context, recipientIDs []string, alert model.Alert) error
}

// RecipientNotifier is the optional push-only surface used when the
// fan-out widens after a post-trigger escalation: the extra responders
// must be reached, but the sender's emergency contacts must not be
// texted a second time. Satisfied by *AlertDispatcher.
type RecipientNotifier interface {
	NotifyRecipients(ctx context.Context, recipientIDs []string, alert model.Alert) error
}

// IdempotencyStore maps X-Idempotency-Key values to finished alerts.
// Satisfied by *repository.IdempotencyRepository.
type IdempotencyStore interface {
	Claim(ctx context.Context, key string) (bool, error)
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// RateLimiter is a per-user SOS trigger limiter.
// Satisfied by *repository.RateLimitRepository.
type RateLimiter interface {
	Allow(ctx context.Context, userID string, limit int, window time.Duration) (bool, error)
}

// EventPublisher publishes SOS events to Kafka.
// Satisfied by *messaging.SOSProducer.
type EventPublisher interface {
	Send(ctx context.Context, event model.SOSEvent) error
}

// TrackHub publishes live-tracking payloads to WebSocket subscribers
// and lets handlers subscribe per alert.
// Satisfied by *tracking.Hub.
type TrackHub interface {
	Publish(alertID string, payload []byte)
	Subscribe(alertID string) (<-chan []byte, func())
}

// TriggerSOSDeps groups the optional dependencies of TriggerSOSUseCase.
// Nil optional fields disable that feature (fail-open, logged) so the
// use case stays testable without live Redis, MySQL, Kafka or push infra.
type TriggerSOSDeps struct {
	Validate          *validator.Validate
	Location          LocationRepository
	Alerts            AlertStore
	Notifier          Notifier
	Idempotency       IdempotencyStore
	RateLimiter       RateLimiter
	Publisher         EventPublisher
	Users             TokenReader
	Hub               TrackHub
	Metrics           SOSMetrics
	Triage            EmergencyTriage
	EmergencyContacts EmergencyContactStore
	MediaStore        MediaStorage
	Transcriber       Transcriber
	GracePeriod       time.Duration
	MaxRecipients     int
	MinRecipients     int
	Radii             []float64
	RateLimitPerHour  int
	RateLimitWindow   time.Duration
	ExpireAfter       time.Duration
}

type TriggerSOSUseCase struct {
	DB   *gorm.DB
	Log  *logrus.Logger
	Deps TriggerSOSDeps
}

// NewTriggerSOSUseCase builds the use case, applying README defaults
// for any tuning knob left unset.
func NewTriggerSOSUseCase(db *gorm.DB, log *logrus.Logger, deps TriggerSOSDeps) *TriggerSOSUseCase {
	if deps.MaxRecipients <= 0 {
		deps.MaxRecipients = 50
	}
	if deps.MinRecipients <= 0 {
		deps.MinRecipients = 3
	}
	if len(deps.Radii) == 0 {
		deps.Radii = []float64{1000, 2000, 5000}
	}
	if deps.RateLimitPerHour <= 0 {
		deps.RateLimitPerHour = 3
	}
	if deps.RateLimitWindow <= 0 {
		deps.RateLimitWindow = time.Hour
	}
	if deps.ExpireAfter <= 0 {
		deps.ExpireAfter = 24 * time.Hour
	}
	return &TriggerSOSUseCase{DB: db, Log: log, Deps: deps}
}

// Execute is the legacy Redis-only trigger (no body, no idempotency key).
// It is kept so existing callers and tests keep working.
func (u *TriggerSOSUseCase) Execute(ctx context.Context, senderID string) (*model.TriggerSOSResult, error) {
	return u.ExecuteWithRequest(ctx, senderID, &model.TriggerSOSRequest{}, "")
}

// ExecuteWithRequest records the alert, finds active recipients with the
// adaptive radius, and dispatches (after the grace period when set).
func (u *TriggerSOSUseCase) ExecuteWithRequest(ctx context.Context, senderID string, req *model.TriggerSOSRequest, idempotencyKey string) (*model.TriggerSOSResult, error) {
	startedAt := time.Now()
	if req == nil {
		req = &model.TriggerSOSRequest{}
	}
	if u.Deps.Validate != nil {
		if err := u.Deps.Validate.Struct(req); err != nil {
			u.Log.Warnf("Invalid SOS request: %+v", err)
			return nil, fiber.ErrBadRequest
		}
	}
	if (req.Latitude == nil) != (req.Longitude == nil) {
		return nil, fiber.ErrBadRequest
	}
	if senderID == "" {
		return nil, model.ErrInvalidSender
	}

	// Anti-abuse: fail open on limiter errors (a life-safety request
	// must not be blocked by a Redis outage), fail closed on limit hit.
	if u.Deps.RateLimiter != nil {
		allowed, err := u.Deps.RateLimiter.Allow(ctx, senderID, u.Deps.RateLimitPerHour, u.Deps.RateLimitWindow)
		if err != nil {
			u.Log.WithError(err).Warn("rate limiter unavailable, proceeding")
		} else if !allowed {
			return nil, model.ErrRateLimited
		}
	}

	// Idempotency: replay a finished key instead of creating a duplicate.
	if idempotencyKey != "" && u.Deps.Idempotency != nil {
		if replay, done, err := u.replayIfExists(ctx, idempotencyKey); err != nil {
			return nil, err
		} else if done {
			return replay, nil
		}
		if claimed, err := u.Deps.Idempotency.Claim(ctx, idempotencyKey); err != nil {
			u.Log.WithError(err).Warn("idempotency claim unavailable, proceeding")
		} else if !claimed {
			if replay, done, err := u.replayIfExists(ctx, idempotencyKey); err != nil {
				return nil, err
			} else if done {
				return replay, nil
			}
			return nil, fiber.ErrConflict
		}
	}

	latitude, longitude, err := u.resolveCoordinates(ctx, senderID, req)
	if err != nil {
		return nil, err
	}

	// Optional AI triage: classify the sender's description into an
	// emergency type, a 0..1 severity (driving the adaptive fan-out
	// start radius) and a short summary. Fail-open: a nil engine, a
	// disabled engine or an error keeps the request/default behaviour,
	// so a life-safety alert never depends on AI.
	triageInfo := model.TriageInfo{}
	if u.Deps.Triage != nil {
		triage, err := u.Deps.Triage.Triage(ctx, TriageInput{Description: req.Description})
		if err != nil {
			u.Log.WithError(err).Warn("AI triage failed, using request defaults")
		} else {
			triageInfo = model.TriageInfo{
				EmergencyType: triage.EmergencyType,
				Severity:      triage.Severity,
				Summary:       triage.Summary,
			}
		}
	}

	// An explicit classification — typically the result of
	// POST /api/sos/classify on the audio clip — wins over the
	// description-based triage, so the very first fan-out already
	// reflects the audio-derived severity.
	if req.Severity != nil {
		triageInfo.Severity = *req.Severity
	}
	if req.Summary != "" {
		triageInfo.Summary = req.Summary
	}

	emergencyType := req.EmergencyType
	if emergencyType == "" {
		if triageInfo.EmergencyType != "" {
			emergencyType = triageInfo.EmergencyType
		} else {
			emergencyType = entity.EmergencyTypeDanger
		}
	}
	radiusStart := 0
	if triageInfo.Severity > 0 {
		radiusStart = u.radiusStartIndex(triageInfo.Severity)
	}
	alert := &entity.Alert{
		ID:                uuid.NewString(),
		SenderID:          senderID,
		Longitude:         longitude,
		Latitude:          latitude,
		TriggeredAt:       time.Now().UnixMilli(),
		Status:            entity.AlertStatusTriggered,
		EmergencyType:     emergencyType,
		AccuracyMeters:    req.AccuracyMeters,
		BatteryPercentage: req.BatteryPercentage,
	}
	if triageInfo.Severity > 0 {
		alert.Severity = &triageInfo.Severity
	}
	if triageInfo.Summary != "" {
		alert.Summary = &triageInfo.Summary
	}
	if err := u.Deps.Alerts.Create(ctx, u.DB, alert); err != nil {
		return nil, fmt.Errorf("save alert: %w", err)
	}
	u.Log.Infof("alert %s saved for sender %s at (%.6f, %.6f)", alert.ID, senderID, longitude, latitude)

	recipients, radius, err := u.findRecipients(ctx, senderID, longitude, latitude, radiusStart)
	if err != nil {
		return nil, err
	}
	if err := u.Deps.Alerts.CreateRecipients(ctx, u.DB, alert.ID, recipients, alert.TriggeredAt); err != nil {
		u.Log.WithError(err).Warn("could not record alert recipients")
	}

	alertModel := model.Alert{
		ID:                alert.ID,
		SenderId:          alert.SenderID,
		TriggeredAt:       time.UnixMilli(alert.TriggeredAt),
		Status:            alert.Status,
		EmergencyType:     alert.EmergencyType,
		AccuracyMeters:    alert.AccuracyMeters,
		BatteryPercentage: alert.BatteryPercentage,
		Severity:          triageInfo.Severity,
		Summary:           triageInfo.Summary,
		Location: model.LocationResponse{
			Longitude: alert.Longitude,
			Latitude:  alert.Latitude,
		},
	}

	if idempotencyKey != "" && u.Deps.Idempotency != nil {
		value := alert.ID + "|" + strconv.Itoa(len(recipients))
		if err := u.Deps.Idempotency.Set(ctx, idempotencyKey, value); err != nil {
			u.Log.WithError(err).Warn("could not store idempotency result")
		}
	}

	result := &model.TriggerSOSResult{
		Alert:              alertModel,
		Recipients:         recipients,
		RadiusMeters:       radius,
		GracePeriodSeconds: int(u.Deps.GracePeriod.Seconds()),
	}
	if triageInfo.EmergencyType != "" || triageInfo.Summary != "" || triageInfo.Severity > 0 {
		result.Triage = &triageInfo
	}

	dispatch := func(dispatchCtx context.Context) {
		if u.Deps.Publisher != nil {
			event := model.SOSEvent{
				EventType:     "sos.dispatched",
				AlertID:       alert.ID,
				SenderID:      senderID,
				Latitude:      latitude,
				Longitude:     longitude,
				EmergencyType: emergencyType,
				Status:        alert.Status,
				Recipients:    recipients,
				TriggeredAt:   alert.TriggeredAt,
				Severity:      triageInfo.Severity,
				Summary:       triageInfo.Summary,
			}
			if err := u.Deps.Publisher.Send(dispatchCtx, event); err == nil {
				u.recordDispatch(startedAt)
				return
			}
			u.Log.Warn("Kafka publish failed, falling back to direct notify")
		}
		if u.Deps.Notifier != nil {
			if err := u.Deps.Notifier.Notify(dispatchCtx, recipients, alertModel); err != nil {
				u.Log.WithError(err).Error("failed to notify recipients")
			}
		}
		u.recordDispatch(startedAt)
	}

	if u.Deps.GracePeriod > 0 {
		// The grace period lets the sender cancel a false alarm before
		// anyone is notified. The scheduled dispatch re-checks the
		// status so a cancelled alert is never fanned out.
		time.AfterFunc(u.Deps.GracePeriod, func() {
			dctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if current, err := u.Deps.Alerts.GetByID(dctx, u.DB, alert.ID); err == nil {
				if current.Status != entity.AlertStatusTriggered {
					u.Log.Infof("alert %s is %s, skipping grace-period dispatch", alert.ID, current.Status)
					return
				}
			} else {
				u.Log.WithError(err).Warnf("could not re-check alert %s before dispatch", alert.ID)
			}
			dispatch(dctx)
		})
	} else {
		dispatch(ctx)
	}

	return result, nil
}

func (u *TriggerSOSUseCase) recordDispatch(startedAt time.Time) {
	if u.Deps.Metrics != nil {
		u.Deps.Metrics.ObserveDispatchDuration(float64(time.Since(startedAt).Milliseconds()))
	}
}

// replayIfExists returns a previously created result for an idempotency
// key, or done=false when the key has no finished result yet.
func (u *TriggerSOSUseCase) replayIfExists(ctx context.Context, key string) (*model.TriggerSOSResult, bool, error) {
	value, err := u.Deps.Idempotency.Get(ctx, key)
	if err != nil {
		u.Log.WithError(err).Warn("idempotency lookup unavailable, proceeding")
		return nil, false, nil
	}
	if value == "" {
		return nil, false, nil
	}
	parts := strings.SplitN(value, "|", 2)
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, parts[0])
	if err != nil {
		return nil, false, fmt.Errorf("load replayed alert: %w", err)
	}
	count := 0
	if len(parts) == 2 {
		count, _ = strconv.Atoi(parts[1])
	}
	recipients := make([]string, count)
	alertModel := model.Alert{
		ID:            alert.ID,
		SenderId:      alert.SenderID,
		TriggeredAt:   time.UnixMilli(alert.TriggeredAt),
		Status:        alert.Status,
		EmergencyType: alert.EmergencyType,
		Location: model.LocationResponse{
			Longitude: alert.Longitude,
			Latitude:  alert.Latitude,
		},
	}
	if alert.Severity != nil {
		alertModel.Severity = *alert.Severity
	}
	if alert.Summary != nil {
		alertModel.Summary = *alert.Summary
	}
	return &model.TriggerSOSResult{
		Alert:              alertModel,
		Recipients:         recipients,
		GracePeriodSeconds: 0,
	}, true, nil
}

// resolveCoordinates prefers explicit body coordinates (and refreshes
// Redis GEO with them), falling back to the last known Redis position.
func (u *TriggerSOSUseCase) resolveCoordinates(ctx context.Context, senderID string, req *model.TriggerSOSRequest) (float64, float64, error) {
	if req.Latitude != nil && req.Longitude != nil {
		if u.Deps.Location != nil {
			if err := u.Deps.Location.Save(ctx, &entity.LocationCache{
				UserId:    senderID,
				Longitude: *req.Longitude,
				Latitude:  *req.Latitude,
			}); err != nil {
				u.Log.WithError(err).Warn("could not refresh Redis location from SOS body")
			}
		}
		return *req.Latitude, *req.Longitude, nil
	}
	sender, err := u.Deps.Location.Get(ctx, senderID)
	if err != nil {
		return 0, 0, fmt.Errorf("get sender location: %w", err)
	}
	return sender.Latitude, sender.Longitude, nil
}

// radiusStartIndex maps an AI severity (0..1) onto the configured
// adaptive radii: more severe incidents start their fan-out broader.
func (u *TriggerSOSUseCase) radiusStartIndex(severity float64) int {
	n := len(u.Deps.Radii)
	if n <= 1 || severity <= 0 {
		return 0
	}
	if severity >= 1 {
		return n - 1
	}
	idx := int(math.Floor(severity * float64(n)))
	if idx >= n {
		idx = n - 1
	}
	return idx
}

// findRecipients runs adaptive-radius GeoSearch: start at the radius
// selected by startIndex (0 = 1 km; an AI severity may start wider) and
// expand upward until at least MinRecipients fresh users are found,
// capping the fan-out at MaxRecipients (nearest first).
func (u *TriggerSOSUseCase) findRecipients(ctx context.Context, senderID string, longitude, latitude float64, startIndex int) ([]string, float64, error) {
	if startIndex < 0 {
		startIndex = 0
	}
	if startIndex >= len(u.Deps.Radii) {
		startIndex = len(u.Deps.Radii) - 1
	}
	now := time.Now().UnixMilli()
	var recipients []string
	usedRadius := u.Deps.Radii[len(u.Deps.Radii)-1]
	for _, radius := range u.Deps.Radii[startIndex:] {
		usedRadius = radius
		nearby, err := u.Deps.Location.GeoSearch(ctx, longitude, latitude, radius)
		if err != nil {
			return nil, 0, fmt.Errorf("search nearby users: %w", err)
		}
		seen := make(map[string]struct{}, len(nearby))
		candidates := make([]string, 0, len(nearby))
		for _, userID := range nearby {
			if userID == senderID {
				continue
			}
			if _, dup := seen[userID]; dup {
				continue
			}
			seen[userID] = struct{}{}
			candidates = append(candidates, userID)
		}
		// One HMGET per radius instead of one HGET per candidate: the
		// old loop paid a Redis round trip per nearby user.
		lastSeenByUser, err := u.Deps.Location.GetLastSeenBatch(ctx, candidates)
		if err != nil {
			u.Log.Warnf("could not read last_seen batch, skipping freshness filter: %v", err)
			lastSeenByUser = nil
		}
		recipients = recipients[:0]
		for _, userID := range candidates {
			lastSeen, ok := lastSeenByUser[userID]
			if !ok {
				u.Log.Warnf("no last_seen for %s, skipping", userID)
				continue
			}
			if now-lastSeen > staleThreshold {
				u.Log.Debugf("skipping stale user %s (last_seen %dms ago)", userID, now-lastSeen)
				continue
			}
			recipients = append(recipients, userID)
			if len(recipients) >= u.Deps.MaxRecipients {
				break
			}
		}
		if len(recipients) >= u.Deps.MinRecipients {
			break
		}
	}
	if recipients == nil {
		recipients = []string{}
	}
	return recipients, usedRadius, nil
}

// escalateFanout widens the responder fan-out after post-trigger audio
// raised the incident severity. Only responders not already recorded
// for the alert are notified, and the notification still respects the
// trigger's grace period so a false alarm cancelled during the countdown
// never reaches anyone new.
func (u *TriggerSOSUseCase) escalateFanout(ctx context.Context, alert *entity.Alert, info model.TriageInfo, previousSeverity float64) (int, error) {
	if u.Deps.Location == nil || u.Deps.Alerts == nil {
		return 0, nil
	}
	oldStart := 0
	if previousSeverity > 0 {
		oldStart = u.radiusStartIndex(previousSeverity)
	}
	newStart := 0
	if info.Severity > 0 {
		newStart = u.radiusStartIndex(info.Severity)
	}
	if newStart <= oldStart {
		return 0, nil
	}

	notified, err := u.Deps.Alerts.ListRecipients(ctx, u.DB, alert.ID)
	if err != nil {
		return 0, fmt.Errorf("list alert recipients: %w", err)
	}
	already := make(map[string]struct{}, len(notified))
	for _, id := range notified {
		already[id] = struct{}{}
	}

	wider, _, err := u.findRecipients(ctx, alert.SenderID, alert.Longitude, alert.Latitude, newStart)
	if err != nil {
		return 0, err
	}
	added := make([]string, 0, len(wider))
	for _, id := range wider {
		if _, seen := already[id]; seen {
			continue
		}
		added = append(added, id)
	}
	if len(added) == 0 {
		return 0, nil
	}
	if err := u.Deps.Alerts.CreateRecipients(ctx, u.DB, alert.ID, added, time.Now().UnixMilli()); err != nil {
		u.Log.WithError(err).Warn("could not record escalated recipients")
	}

	emergencyType := alert.EmergencyType
	if info.EmergencyType != "" {
		emergencyType = info.EmergencyType
	}
	escalated := model.Alert{
		ID:            alert.ID,
		SenderId:      alert.SenderID,
		TriggeredAt:   time.UnixMilli(alert.TriggeredAt),
		Status:        alert.Status,
		EmergencyType: emergencyType,
		Severity:      info.Severity,
		Summary:       info.Summary,
		Location: model.LocationResponse{
			Longitude: alert.Longitude,
			Latitude:  alert.Latitude,
		},
	}

	notify := func(nctx context.Context) {
		// Mirror the trigger dispatch: re-check the status so a cancelled
		// or resolved incident is never fanned out.
		if current, err := u.Deps.Alerts.GetByID(nctx, u.DB, alert.ID); err == nil && current != nil {
			if !current.IsActive() {
				u.Log.Infof("alert %s is %s, skipping escalated fan-out", alert.ID, current.Status)
				return
			}
		}
		if u.Deps.Publisher != nil {
			event := model.SOSEvent{
				EventType:     "sos.dispatched",
				AlertID:       alert.ID,
				SenderID:      alert.SenderID,
				Latitude:      alert.Latitude,
				Longitude:     alert.Longitude,
				EmergencyType: emergencyType,
				Status:        alert.Status,
				Recipients:    added,
				TriggeredAt:   alert.TriggeredAt,
				Severity:      info.Severity,
				Summary:       info.Summary,
			}
			if err := u.Deps.Publisher.Send(nctx, event); err == nil {
				return
			}
			u.Log.Warn("Kafka publish failed for escalation, falling back to direct notify")
		}
		u.notifyNewRecipients(nctx, added, escalated)
	}

	remaining := u.Deps.GracePeriod - time.Duration(time.Now().UnixMilli()-alert.TriggeredAt)*time.Millisecond
	if remaining > 0 {
		time.AfterFunc(remaining, func() {
			nctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			notify(nctx)
		})
		return len(added), nil
	}
	notify(ctx)
	return len(added), nil
}

// notifyNewRecipients pushes an escalated classification to responders
// who were not part of the original fan-out. The push-only surface is
// preferred so the sender's emergency contacts are not texted twice.
func (u *TriggerSOSUseCase) notifyNewRecipients(ctx context.Context, recipients []string, alert model.Alert) {
	if u.Deps.Notifier == nil || len(recipients) == 0 {
		return
	}
	if rn, ok := u.Deps.Notifier.(RecipientNotifier); ok {
		if err := rn.NotifyRecipients(ctx, recipients, alert); err != nil {
			u.Log.WithError(err).Error("failed to notify escalated recipients")
		}
		return
	}
	if err := u.Deps.Notifier.Notify(ctx, recipients, alert); err != nil {
		u.Log.WithError(err).Error("failed to notify escalated recipients")
	}
}
