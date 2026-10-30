package usecase

import (
	"context"
	"fmt"
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
	GetLastSeen(ctx context.Context, userId string) (int64, error)
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
	EmergencyContacts EmergencyContactStore
	MediaStore        MediaStorage
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

	emergencyType := req.EmergencyType
	if emergencyType == "" {
		emergencyType = entity.EmergencyTypeDanger
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
	if err := u.Deps.Alerts.Create(ctx, u.DB, alert); err != nil {
		return nil, fmt.Errorf("save alert: %w", err)
	}
	u.Log.Infof("alert %s saved for sender %s at (%.6f, %.6f)", alert.ID, senderID, longitude, latitude)

	recipients, radius, err := u.findRecipients(ctx, senderID, longitude, latitude)
	if err != nil {
		return nil, err
	}

	alertModel := model.Alert{
		ID:                alert.ID,
		SenderId:          alert.SenderID,
		TriggeredAt:       time.UnixMilli(alert.TriggeredAt),
		Status:            alert.Status,
		EmergencyType:     alert.EmergencyType,
		AccuracyMeters:    alert.AccuracyMeters,
		BatteryPercentage: alert.BatteryPercentage,
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
	return &model.TriggerSOSResult{
		Alert: model.Alert{
			ID:            alert.ID,
			SenderId:      alert.SenderID,
			TriggeredAt:   time.UnixMilli(alert.TriggeredAt),
			Status:        alert.Status,
			EmergencyType: alert.EmergencyType,
			Location: model.LocationResponse{
				Longitude: alert.Longitude,
				Latitude:  alert.Latitude,
			},
		},
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

// findRecipients runs adaptive-radius GeoSearch: start at 1 km and
// expand to 2 km / 5 km until at least MinRecipients fresh users are
// found, capping the fan-out at MaxRecipients (nearest first).
func (u *TriggerSOSUseCase) findRecipients(ctx context.Context, senderID string, longitude, latitude float64) ([]string, float64, error) {
	now := time.Now().UnixMilli()
	var recipients []string
	usedRadius := u.Deps.Radii[len(u.Deps.Radii)-1]
	for _, radius := range u.Deps.Radii {
		usedRadius = radius
		nearby, err := u.Deps.Location.GeoSearch(ctx, longitude, latitude, radius)
		if err != nil {
			return nil, 0, fmt.Errorf("search nearby users: %w", err)
		}
		seen := make(map[string]struct{}, len(nearby))
		recipients = recipients[:0]
		for _, userID := range nearby {
			if userID == senderID {
				continue
			}
			if _, dup := seen[userID]; dup {
				continue
			}
			seen[userID] = struct{}{}
			lastSeen, err := u.Deps.Location.GetLastSeen(ctx, userID)
			if err != nil {
				u.Log.Warnf("could not get last_seen for %s, skipping: %v", userID, err)
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
