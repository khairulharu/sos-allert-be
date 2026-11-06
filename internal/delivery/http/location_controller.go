package http

import (
	"strconv"

	"golang-clean-architecture/internal/delivery/http/middleware"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/usecase"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

type LocationController struct {
	Log     *logrus.Logger
	UseCase *usecase.LocationUsecase
}

func NewLocationController(useCase *usecase.LocationUsecase, log *logrus.Logger) *LocationController {
	return &LocationController{
		Log:     log,
		UseCase: useCase,
	}
}

func (l *LocationController) Get(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)

	response, err := l.UseCase.GetLocation(ctx.UserContext(), auth.ID)
	if err != nil {
		l.Log.WithError(err).Warn("error getting location")
		return err
	}

	return ctx.JSON(model.WebResponse[*model.LocationStatusResponse]{Data: response})
}

func (l *LocationController) ListNearby(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)

	radiusMeters := 0
	if raw := ctx.Query("radius_m"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return fiber.ErrBadRequest
		}
		radiusMeters = parsed
	}

	response, err := l.UseCase.ListNearby(ctx.UserContext(), auth.ID, float64(radiusMeters))
	if err != nil {
		l.Log.WithError(err).Warn("error listing nearby responders")
		return err
	}

	return ctx.JSON(model.WebResponse[*model.NearbyLocationResponse]{Data: response})
}

func (l *LocationController) Update(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)

	request := new(model.UpdateLocationRequest)
	if err := ctx.BodyParser(request); err != nil {
		l.Log.WithError(err).Error("error parsing request body")
		return fiber.ErrBadRequest
	}

	request.UserId = auth.ID

	response, err := l.UseCase.UpdateLocation(ctx.UserContext(), request)
	if err != nil {
		l.Log.WithError(err).Error("error updating location")
		return err
	}

	return ctx.JSON(model.WebResponse[bool]{Data: response})
}
