package repository

import (
	"context"
	"fmt"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

type LocationCacheRepository struct {
	Client *redis.Client
	Log    *logrus.Logger
}

func NewLocationCacheRepository(client *redis.Client, log *logrus.Logger) *LocationCacheRepository {
	return &LocationCacheRepository{
		Client: client,
		Log:    log,
	}
}

func (l *LocationCacheRepository) Save(ctx context.Context, loc *entity.LocationCache) error {
	if err := l.Client.GeoAdd(ctx, userLocationKey, &redis.GeoLocation{
		Name:      loc.UserId,
		Longitude: loc.Longitude,
		Latitude:  loc.Latitude,
	}).Err(); err != nil {
		return fmt.Errorf("redis geoadd: %w", err)
	}

	nowMs := time.Now().UnixMilli()
	if err := l.Client.HSet(ctx, userLastSeenKey, loc.UserId, nowMs).Err(); err != nil {
		return fmt.Errorf("redis hset last_seen: %w", err)
	}

	return nil
}

func (l *LocationCacheRepository) GetLastSeen(ctx context.Context, userId string) (int64, error) {
	val, err := l.Client.HGet(ctx, userLastSeenKey, userId).Int64()
	if err != nil {
		return 0, fmt.Errorf("redis hget last_seen %s: %w", userId, err)
	}
	return val, nil
}

// GetLastSeenBatch reads the last_seen timestamp for many users in one
// Redis round trip (HMGET). Callers that filter a candidate list use it
// instead of one GetLastSeen per candidate. Users with no stored
// heartbeat are simply absent from the result map.
func (l *LocationCacheRepository) GetLastSeenBatch(ctx context.Context, userIds []string) (map[string]int64, error) {
	out := make(map[string]int64, len(userIds))
	if len(userIds) == 0 {
		return out, nil
	}
	values, err := l.Client.HMGet(ctx, userLastSeenKey, userIds...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis hmget last_seen: %w", err)
	}
	for i, value := range values {
		if value == nil {
			continue
		}
		switch v := value.(type) {
		case int64:
			out[userIds[i]] = v
		case string:
			if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
				out[userIds[i]] = parsed
			}
		}
	}
	return out, nil
}

func (l *LocationCacheRepository) Get(ctx context.Context, userId string) (*entity.LocationCache, error) {
	positions, err := l.Client.GeoPos(ctx, userLocationKey, userId).Result()
	if err != nil {
		return nil, fmt.Errorf("redis geopos: %w", err)
	}

	if len(positions) == 0 || positions[0] == nil {
		return nil, model.ErrUserLocationNotFound
	}

	return &entity.LocationCache{
		UserId:    userId,
		Longitude: positions[0].Longitude,
		Latitude:  positions[0].Latitude,
	}, nil
}

func (l *LocationCacheRepository) GeoSearch(ctx context.Context, longitude float64, latitude float64, radiusMeters float64) ([]string, error) {
	userIDs, err := l.Client.GeoSearch(ctx, userLocationKey, &redis.GeoSearchQuery{
		Longitude:  longitude,
		Latitude:   latitude,
		Radius:     radiusMeters,
		RadiusUnit: "m",
		Sort:       "ASC",
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("redis geosearch: %w", err)
	}

	return userIDs, nil
}

// GeoSearchWithDistance returns positions and distances for a radius
// search, sorted nearest first. Used by GET /api/location/nearby.
func (l *LocationCacheRepository) GeoSearchWithDistance(ctx context.Context, longitude float64, latitude float64, radiusMeters float64, count int) ([]entity.GeoLocation, error) {
	query := &redis.GeoSearchLocationQuery{
		GeoSearchQuery: redis.GeoSearchQuery{
			Longitude:  longitude,
			Latitude:   latitude,
			Radius:     radiusMeters,
			RadiusUnit: "m",
			Sort:       "ASC",
		},
		WithCoord: true,
		WithDist:  true,
	}
	if count > 0 {
		query.Count = count
	}

	results, err := l.Client.GeoSearchLocation(ctx, userLocationKey, query).Result()
	if err != nil {
		return nil, fmt.Errorf("redis geosearch location: %w", err)
	}

	out := make([]entity.GeoLocation, 0, len(results))
	for _, result := range results {
		out = append(out, entity.GeoLocation{
			UserId:         result.Name,
			Longitude:      result.Longitude,
			Latitude:       result.Latitude,
			DistanceMeters: result.Dist,
		})
	}
	return out, nil
}

func (l *LocationCacheRepository) Delete(ctx context.Context, userId string) (bool, error) {
	if err := l.Client.ZRem(ctx, userLocationKey, userId).Err(); err != nil {
		return false, fmt.Errorf("redis zrem: %w", err)
	}
	return true, nil
}

func (r *LocationCacheRepository) GetAll(ctx context.Context) ([]*entity.LocationCache, error) {
	userIDs, err := r.Client.ZRange(ctx, userLocationKey, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("redis zrange: %w", err)
	}

	if len(userIDs) == 0 {
		return nil, nil
	}

	positions, err := r.Client.GeoPos(ctx, userLocationKey, userIDs...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis geopos: %w", err)
	}

	result := make([]*entity.LocationCache, 0, len(userIDs))
	for i, pos := range positions {
		if pos == nil {
			continue
		}

		result = append(result, &entity.LocationCache{
			UserId:    userIDs[i],
			Longitude: pos.Longitude,
			Latitude:  pos.Latitude,
		})
	}

	return result, nil
}
