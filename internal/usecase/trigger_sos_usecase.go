package usecase

import (
	"context"
	"fmt"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const sosRadiusMeters = 1000.0
const staleThreshold = 2 * 60 * 1000 // 2 minutes in milliseconds

// LocationRepository is the subset of location storage used by TriggerSOS.
// It is satisfied by *repository.LocationCacheRepository (Redis GEO).
type LocationRepository interface {
	Get(ctx context.Context, userId string) (*entity.LocationCache, error)
	GeoSearch(ctx context.Context, longitude float64, latitude float64, radiusMeters float64) ([]string, error)
	GetLastSeen(ctx context.Context, userId string) (int64, error)
}

// AlertSaver persists an alert record. Satisfied by *repository.AlertRepository.
type AlertSaver interface {
	Create(ctx context.Context, db *gorm.DB, alert *entity.Alert) error
}

// Notifier sends SOS notifications to recipients.
// Satisfied by *AlertNotifier.
type Notifier interface {
	Notify(ctx context.Context, recipientIDs []string, alert model.Alert) error
}

type TriggerSOSUseCase struct {
	DB                 *gorm.DB
	Log                *logrus.Logger
	LocationRepository LocationRepository
	AlertRepository    AlertSaver
	Notifier           Notifier
}

func NewTriggerSOSUseCase(
	db *gorm.DB,
	log *logrus.Logger,
	locationRepository LocationRepository,
	alertRepository AlertSaver,
	notifier Notifier,
) *TriggerSOSUseCase {
	return &TriggerSOSUseCase{
		DB:                 db,
		Log:                log,
		LocationRepository: locationRepository,
		AlertRepository:    alertRepository,
		Notifier:           notifier,
	}
}

// Execute records the alert to MySQL, finds active recipients within 1 km, and notifies them.
func (u *TriggerSOSUseCase) Execute(ctx context.Context, senderID string) (*model.TriggerSOSResult, error) {
	sender, err := u.LocationRepository.Get(ctx, senderID)
	if err != nil {
		return nil, fmt.Errorf("get sender location: %w", err)
	}

	alert := &entity.Alert{
		ID:          uuid.NewString(),
		SenderID:    senderID,
		Longitude:   sender.Longitude,
		Latitude:    sender.Latitude,
		TriggeredAt: time.Now().UnixMilli(),
	}

	if err := u.AlertRepository.Create(ctx, u.DB, alert); err != nil {
		return nil, fmt.Errorf("save alert: %w", err)
	}

	u.Log.Infof("alert %s saved for sender %s at (%.6f, %.6f)", alert.ID, senderID, sender.Longitude, sender.Latitude)

	nearbyUserIDs, err := u.LocationRepository.GeoSearch(ctx, sender.Longitude, sender.Latitude, sosRadiusMeters)
	if err != nil {
		return nil, fmt.Errorf("search nearby users: %w", err)
	}

	now := time.Now().UnixMilli()
	recipients := make([]string, 0, len(nearbyUserIDs))
	for _, userId := range nearbyUserIDs {
		if userId == senderID {
			continue
		}

		lastSeen, err := u.LocationRepository.GetLastSeen(ctx, userId)
		if err != nil {
			u.Log.Warnf("could not get last_seen for %s, skipping: %v", userId, err)
			continue
		}
		if now-lastSeen > staleThreshold {
			u.Log.Debugf("skipping stale user %s (last_seen %dms ago)", userId, now-lastSeen)
			continue
		}

		recipients = append(recipients, userId)
	}

	alertModel := model.Alert{
		ID:          alert.ID,
		SenderId:    alert.SenderID,
		TriggeredAt: time.UnixMilli(alert.TriggeredAt),
		Location: model.LocationResponse{
			Longitude: alert.Longitude,
			Latitude:  alert.Latitude,
		},
	}

	if err := u.Notifier.Notify(ctx, recipients, alertModel); err != nil {
		u.Log.WithError(err).Error("failed to notify recipients")
	}

	return &model.TriggerSOSResult{
		Alert:      alertModel,
		Recipients: recipients,
	}, nil
}
