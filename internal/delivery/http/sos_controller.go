package http

import (
	"golang-clean-architecture/internal/delivery/http/middleware"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/usecase"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

type SOSController struct {
	Log     *logrus.Logger
	UseCase *usecase.TriggerSOSUseCase
}

func NewSOSController(useCase *usecase.TriggerSOSUseCase, log *logrus.Logger) *SOSController {
	return &SOSController{
		Log:     log,
		UseCase: useCase,
	}
}

func (s *SOSController) Trigger(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)

	result, err := s.UseCase.Execute(ctx.UserContext(), auth.ID)
	if err != nil {
		s.Log.WithError(err).Error("error triggering SOS")
		return err
	}

	return ctx.JSON(model.WebResponse[*model.TriggerSOSResult]{Data: result})
}
