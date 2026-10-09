package http

import (
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

func (l *LocationController) Update(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)

	request := new(model.UpdateLocationRequest)
	if err := ctx.BodyParser(request); err != nil {
		l.Log.WithError(err).Error("error parsing request body")
		return fiber.ErrBadRequest
	}

	request.UserId = auth.ID

	err := l.UseCase.UpdateLocation(ctx.UserContext(), request)
	if err != nil {
		l.Log.WithError(err).Error("error updating location")
		return err
	}

	return ctx.JSON(model.WebResponse[bool]{Data: true})
}
