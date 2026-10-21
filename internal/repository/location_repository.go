package repository

import (
	"golang-clean-architecture/internal/entity"

	"github.com/sirupsen/logrus"
)

type LocationRepository struct {
	Repository[entity.Location]
	Log *logrus.Logger
}

func NewLocationRepository(log *logrus.Logger) *LocationRepository {
	return &LocationRepository{
		Log: log,
	}
}
