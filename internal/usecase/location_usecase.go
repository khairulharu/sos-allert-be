package usecase

import (
	"context"
	"errors"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

const (
	defaultNearbyRadiusMeters = 1000
	minNearbyRadiusMeters     = 100
	maxNearbyRadiusMeters     = 5000
	maxNearbyResponders       = 50
	nearbySearchCount         = 100
)

// LocationHistoryStore is the MySQL side of location persistence
// (the historical `locations` table). Satisfied by
// *repository.LocationRepository.
type LocationHistoryStore interface {
	Upsert(ctx context.Context, userId string, longitude, latitude float64) error
}

type LocationUsecase struct {
	LocationCacheRepository LocationRepository
	LocationHistory         LocationHistoryStore
	Validate                *validator.Validate
	Log                     *logrus.Logger
}

func NewLocationUseCase(locationCacheRepository LocationRepository, locationHistory LocationHistoryStore, validate *validator.Validate, log *logrus.Logger) *LocationUsecase {
	return &LocationUsecase{
		LocationCacheRepository: locationCacheRepository,
		LocationHistory:         locationHistory,
		Log:                     log,
		Validate:                validate,
	}
}

func (l *LocationUsecase) UpdateLocation(ctx context.Context, request *model.UpdateLocationRequest) (bool, error) {
	if err := l.Validate.Struct(request); err != nil {
		l.Log.Warnf("Invalid request body: %+v", err)
		return false, fiber.ErrBadRequest
	}

	userLocation := &entity.LocationCache{
		UserId:    request.UserId,
		Longitude: *request.Longitude,
		Latitude:  *request.Latitude,
	}

	if err := l.LocationCacheRepository.Save(ctx, userLocation); err != nil {
		l.Log.Warnf("Failed to update user location: %+v", err)
		return false, fiber.ErrInternalServerError
	}

	// Redis is the operational store used for SOS fan-out. MySQL keeps one
	// historical row per user; a failure here must not fail the heartbeat.
	l.syncLocationHistory(ctx, request)

	return true, nil
}

func (l *LocationUsecase) syncLocationHistory(ctx context.Context, request *model.UpdateLocationRequest) {
	if l.LocationHistory == nil {
		return
	}
	if err := l.LocationHistory.Upsert(ctx, request.UserId, *request.Longitude, *request.Latitude); err != nil {
		l.Log.Warnf("Failed to upsert location history for %s: %+v", request.UserId, err)
	}
}

// GetLocation returns the authenticated user's last heartbeat. It is the
// read side of PUT /api/location so clients can verify that sharing works.
func (l *LocationUsecase) GetLocation(ctx context.Context, userId string) (*model.LocationStatusResponse, error) {
	location, err := l.LocationCacheRepository.Get(ctx, userId)
	if err != nil {
		if errors.Is(err, model.ErrUserLocationNotFound) {
			return nil, fiber.ErrNotFound
		}
		l.Log.Warnf("Failed to get user location: %+v", err)
		return nil, fiber.ErrInternalServerError
	}
	if location == nil {
		return nil, fiber.ErrNotFound
	}

	lastSeen, err := l.LocationCacheRepository.GetLastSeen(ctx, userId)
	if err != nil {
		// The GEO entry and last_seen hash are written together, but treat a
		// missing timestamp as unknown instead of failing the read.
		l.Log.Debugf("No last_seen timestamp for user %s: %+v", userId, err)
		lastSeen = 0
	}

	return &model.LocationStatusResponse{
		UserId:    location.UserId,
		Latitude:  location.Latitude,
		Longitude: location.Longitude,
		LastSeen:  lastSeen,
		Stale:     lastSeen == 0 || time.Now().UnixMilli()-lastSeen > staleThreshold,
	}, nil
}

// ListNearby returns active users around the requester for the responder
// map. The requester must have a heartbeat, stale users are dropped, and
// coordinates are rounded (~100 m) so the endpoint never leaks a precise
// home/office position.
func (l *LocationUsecase) ListNearby(ctx context.Context, userId string, radiusMeters float64) (*model.NearbyLocationResponse, error) {
	if radiusMeters == 0 {
		radiusMeters = defaultNearbyRadiusMeters
	}
	if radiusMeters < minNearbyRadiusMeters || radiusMeters > maxNearbyRadiusMeters {
		return nil, fiber.ErrBadRequest
	}

	own, err := l.LocationCacheRepository.Get(ctx, userId)
	if err != nil {
		if errors.Is(err, model.ErrUserLocationNotFound) {
			return nil, fiber.ErrNotFound
		}
		l.Log.Warnf("Failed to get requester location: %+v", err)
		return nil, fiber.ErrInternalServerError
	}
	if own == nil {
		return nil, fiber.ErrNotFound
	}

	results, err := l.LocationCacheRepository.GeoSearchWithDistance(ctx, own.Longitude, own.Latitude, radiusMeters, nearbySearchCount)
	if err != nil {
		l.Log.Warnf("Failed to search nearby users: %+v", err)
		return nil, fiber.ErrInternalServerError
	}

	now := time.Now().UnixMilli()
	responders := make([]model.NearbyResponder, 0, len(results))
	for _, result := range results {
		if result.UserId == userId {
			continue
		}
		lastSeen, err := l.LocationCacheRepository.GetLastSeen(ctx, result.UserId)
		if err != nil || lastSeen == 0 || now-lastSeen > staleThreshold {
			continue
		}

		latitude, longitude := model.MaskCoordinates(result.Latitude, result.Longitude)
		responders = append(responders, model.NearbyResponder{
			UserId:         result.UserId,
			Latitude:       latitude,
			Longitude:      longitude,
			DistanceMeters: int(result.DistanceMeters + 0.5),
			LastSeen:       lastSeen,
		})
		if len(responders) >= maxNearbyResponders {
			break
		}
	}

	return &model.NearbyLocationResponse{
		RadiusMeters: radiusMeters,
		Count:        len(responders),
		Responders:   responders,
		Masked:       true,
	}, nil
}
