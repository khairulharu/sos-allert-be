package http

import (
	"golang-clean-architecture/internal/delivery/http/middleware"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/usecase"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

// PushController serves browser Web Push subscription management.
type PushController struct {
	Log     *logrus.Logger
	UseCase *usecase.PushUseCase
}

func NewPushController(useCase *usecase.PushUseCase, log *logrus.Logger) *PushController {
	return &PushController{Log: log, UseCase: useCase}
}

// VAPIDPublicKey returns the application server key a browser needs to
// create a push subscription. 503 when the channel is not configured.
func (c *PushController) VAPIDPublicKey(ctx *fiber.Ctx) error {
	key, err := c.UseCase.VAPIDPublicKey()
	if err != nil {
		return err
	}
	return ctx.JSON(model.WebResponse[*model.PushVAPIDKeyResponse]{Data: &model.PushVAPIDKeyResponse{PublicKey: key}})
}

// Subscribe stores the browser push subscription for the current user.
func (c *PushController) Subscribe(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	request := new(model.PushSubscribeRequest)
	if err := ctx.BodyParser(request); err != nil {
		return fiber.ErrBadRequest
	}
	request.UserID = auth.ID
	response, err := c.UseCase.Subscribe(ctx.UserContext(), request)
	if err != nil {
		return err
	}
	return ctx.JSON(model.WebResponse[*model.PushSubscriptionResponse]{Data: response})
}

// Unsubscribe removes the browser push subscription for one endpoint.
func (c *PushController) Unsubscribe(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	request := new(model.PushUnsubscribeRequest)
	if err := ctx.BodyParser(request); err != nil {
		return fiber.ErrBadRequest
	}
	request.UserID = auth.ID
	if err := c.UseCase.Unsubscribe(ctx.UserContext(), request); err != nil {
		return err
	}
	return ctx.JSON(model.WebResponse[bool]{Data: true})
}

// List returns the current user's registered browser subscriptions.
func (c *PushController) List(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	subs, err := c.UseCase.List(ctx.UserContext(), auth.ID)
	if err != nil {
		return err
	}
	return ctx.JSON(model.WebResponse[[]model.PushSubscriptionResponse]{Data: subs})
}
