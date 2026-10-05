package repository

import (
	"context"
	"fmt"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

type LocationRepository struct {
	Client *redis.Client
	Log    *logrus.Logger
}

func NewLocationRepository(client *redis.Client, log *logrus.Logger) *LocationRepository {
	return &LocationRepository{
		Client: client,
		Log:    log,
	}
}

func (l *LocationRepository) Save(ctx context.Context, loc *entity.UserLocation) error {
	err := l.Client.GeoAdd(ctx, userLocationKey, &redis.GeoLocation{
		Name:      loc.UserId,
		Longitude: loc.Longitude,
		Latitude:  loc.Latitude,
	}).Err()
	if err != nil {
		return fmt.Errorf("redis geoadd: %w", err)
	}
	return nil
}

func (l *LocationRepository) Get(ctx context.Context, userId string) (*entity.UserLocation, error) {
	positions, err := l.Client.GeoPos(ctx, userLocationKey, userId).Result()
	if err != nil {
		return nil, fmt.Errorf("redis geopos: %w", err)
	}

	if len(positions) == 0 || positions[0] == nil {
		return nil, model.ErrUserLocationNotFound
	}

	return &entity.UserLocation{
		UserId:    userId,
		Longitude: positions[0].Longitude,
		Latitude:  positions[0].Latitude,
	}, nil
}

func (l *LocationRepository) Delete(ctx context.Context, userId string) (bool, error) {
	if err := l.Client.ZRem(ctx, userLocationKey, userId).Err(); err != nil {
		return false, fmt.Errorf("redis zrem: %w", err)
	}
	return true, nil
}

func (r *LocationRepository) GetAll(ctx context.Context) ([]*entity.UserLocation, error) {
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

	result := make([]*entity.UserLocation, 0, len(userIDs))
	for i, pos := range positions {
		if pos == nil {
			continue
		}

		result = append(result, &entity.UserLocation{
			UserId:    userIDs[i],
			Longitude: pos.Latitude,
			Latitude:  pos.Latitude,
		})
	}

	return result, nil
}
