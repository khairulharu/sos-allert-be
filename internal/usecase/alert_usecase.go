package usecase

import (
	"golang-clean-architecture/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type AllertUseCase struct {
	DB                      *gorm.DB
	Log                     *logrus.Logger
	Validate                *validator.Validate
	UserRepository          *repository.UserRepository
	LocationRepository      *repository.LocationRepository
	LocationCacheRepository *repository.LocationCacheRepository
}

func NewAllertUseCase(db *gorm.DB, log *logrus.Logger, validate *validator.Validate, userRepository *repository.UserRepository,
	locationRepository *repository.LocationRepository, locationCacheRepository *repository.LocationCacheRepository) *AllertUseCase {
	return &AllertUseCase{
		DB:                      db,
		Log:                     log,
		Validate:                validate,
		UserRepository:          userRepository,
		LocationRepository:      locationRepository,
		LocationCacheRepository: locationCacheRepository,
	}
}

func (a *AllertUseCase) ExecuteAllert(userId string) error {
	// implement validator
	// check timeout location
	// get 1km radius
	return nil
}
