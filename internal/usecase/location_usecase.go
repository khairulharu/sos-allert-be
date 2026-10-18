package usecase

import (
	"context"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

type LocationUsecase struct {
	LocationRepository *repository.LocationRepository
	Validate           *validator.Validate
	Log                *logrus.Logger
}

func NewLocationUseCase(locationRepository *repository.LocationRepository, validate *validator.Validate, log *logrus.Logger) *LocationUsecase {
	return &LocationUsecase{
		LocationRepository: locationRepository,
		Log:                log,
		Validate:           validate,
	}
}

func (l *LocationUsecase) UpdateLocation(ctx context.Context, request *model.UpdateLocationRequest) (bool, error) {
	if err := l.Validate.Struct(request); err != nil {
		l.Log.Warnf("Invalid request body: %+v", err)
		return false, fiber.ErrBadRequest
	}

	userLocation := &entity.UserLocation{
		UserId:    request.UserId,
		Longitude: *request.Longitude,
		Latitude:  *request.Latitude,
	}

	if err := l.LocationRepository.Save(ctx, userLocation); err != nil {
		l.Log.Warnf("Failed to update user location: %+v", err)
		return false, fiber.ErrInternalServerError
	}

	return true, nil
}
