package repository

import (
	"context"

	"golang-clean-architecture/internal/entity"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type LocationRepository struct {
	Repository[entity.Location]
	Log *logrus.Logger
}

func NewLocationRepository(db *gorm.DB, log *logrus.Logger) *LocationRepository {
	return &LocationRepository{
		Repository: Repository[entity.Location]{DB: db},
		Log:        log,
	}
}

// Upsert keeps exactly one historical location row per user. The unique
// index on locations.user_id (migration 20261107000000) makes this safe
// under concurrent heartbeats.
func (r *LocationRepository) Upsert(ctx context.Context, userId string, longitude, latitude float64) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"longitude", "latitude", "updated_at"}),
	}).Create(&entity.Location{
		ID:        uuid.NewString(),
		UserId:    userId,
		Longitude: longitude,
		Latitude:  latitude,
	}).Error
}
