package http

import (
	"golang-clean-architecture/internal/delivery/http/middleware"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/usecase"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

// EmergencyContactController serves Tier-1 contacts for SMS fallback.
type EmergencyContactController struct {
	Log     *logrus.Logger
	UseCase *usecase.TriggerSOSUseCase
}

func NewEmergencyContactController(useCase *usecase.TriggerSOSUseCase, log *logrus.Logger) *EmergencyContactController {
	return &EmergencyContactController{Log: log, UseCase: useCase}
}

func (c *EmergencyContactController) Create(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	request := new(model.CreateEmergencyContactRequest)
	if err := ctx.BodyParser(request); err != nil {
		return fiber.ErrBadRequest
	}
	request.UserID = auth.ID
	response, err := c.UseCase.CreateEmergencyContact(ctx.UserContext(), request)
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[*model.EmergencyContactResponse]{Data: response})
}

func (c *EmergencyContactController) List(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	contacts, err := c.UseCase.ListEmergencyContacts(ctx.UserContext(), auth.ID)
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[[]model.EmergencyContactResponse]{Data: contacts})
}
