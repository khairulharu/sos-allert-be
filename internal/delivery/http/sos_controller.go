package http

import (
	"errors"

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

// mapSOSError converts SOS sentinel errors to HTTP errors. Fiber errors
// and other errors pass through to the global ErrorHandler.
func mapSOSError(err error) error {
	switch {
	case errors.Is(err, model.ErrAlertNotFound):
		return fiber.ErrNotFound
	case errors.Is(err, model.ErrInvalidTransition):
		return fiber.ErrConflict
	case errors.Is(err, model.ErrInvalidPIN):
		return fiber.ErrUnauthorized
	case errors.Is(err, model.ErrPINNotConfigured):
		return fiber.NewError(fiber.StatusPreconditionFailed, err.Error())
	case errors.Is(err, model.ErrRateLimited):
		return fiber.NewError(fiber.StatusTooManyRequests, err.Error())
	case errors.Is(err, model.ErrNotAlertOwner),
		errors.Is(err, model.ErrNotAlertParticipant):
		return fiber.ErrForbidden
	default:
		return err
	}
}

func (s *SOSController) Trigger(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)

	request := new(model.TriggerSOSRequest)
	if len(ctx.Body()) > 0 {
		if err := ctx.BodyParser(request); err != nil {
			s.Log.WithError(err).Error("error parsing SOS request body")
			return fiber.ErrBadRequest
		}
	}

	result, err := s.UseCase.ExecuteWithRequest(ctx.UserContext(), auth.ID, request, ctx.Get("X-Idempotency-Key"))
	if err != nil {
		s.Log.WithError(err).Error("error triggering SOS")
		return mapSOSError(err)
	}

	data := &model.SOSCreatedResponse{
		AlertID:            result.Alert.ID,
		Status:             result.Alert.Status,
		GracePeriodSeconds: result.GracePeriodSeconds,
		RecipientsNotified: len(result.Recipients),
		TriggeredAt:        result.Alert.TriggeredAt.UnixMilli(),
	}
	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.SOSCreatedResponse]{Data: data})
}

func (s *SOSController) Get(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	detail, err := s.UseCase.GetAlert(ctx.UserContext(), auth.ID, ctx.Params("alertId"))
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[*model.AlertDetailResponse]{Data: detail})
}

func (s *SOSController) ListActive(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	alerts, err := s.UseCase.ListActive(ctx.UserContext(), auth.ID)
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[[]*model.AlertDetailResponse]{Data: alerts})
}

func (s *SOSController) Cancel(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	request := new(model.CancelSOSRequest)
	if err := ctx.BodyParser(request); err != nil {
		return fiber.ErrBadRequest
	}
	result, err := s.UseCase.Cancel(ctx.UserContext(), auth.ID, ctx.Params("alertId"), request)
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[*model.CancelSOSResponse]{Data: result})
}

func (s *SOSController) Acknowledge(ctx *fiber.Ctx) error {
	detail, err := s.UseCase.Acknowledge(ctx.UserContext(), ctx.Params("alertId"))
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[*model.AlertDetailResponse]{Data: detail})
}

func (s *SOSController) Resolve(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	detail, err := s.UseCase.Resolve(ctx.UserContext(), auth.ID, ctx.Params("alertId"))
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[*model.AlertDetailResponse]{Data: detail})
}

func (s *SOSController) Respond(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	request := new(model.RespondSOSRequest)
	if err := ctx.BodyParser(request); err != nil {
		return fiber.ErrBadRequest
	}
	result, err := s.UseCase.Respond(ctx.UserContext(), auth.ID, ctx.Params("alertId"), request)
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[*model.RespondSOSResponse]{Data: result})
}

func (s *SOSController) CreateBreadcrumb(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	request := new(model.BreadcrumbRequest)
	if err := ctx.BodyParser(request); err != nil {
		return fiber.ErrBadRequest
	}
	result, err := s.UseCase.AddBreadcrumb(ctx.UserContext(), auth.ID, ctx.Params("alertId"), request)
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[*model.BreadcrumbResponse]{Data: result})
}

func (s *SOSController) ListBreadcrumbs(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	crumbs, err := s.UseCase.ListBreadcrumbs(ctx.UserContext(), auth.ID, ctx.Params("alertId"))
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[[]model.BreadcrumbResponse]{Data: crumbs})
}

func (s *SOSController) UploadMedia(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	file, err := ctx.FormFile("file")
	if err != nil {
		return fiber.ErrBadRequest
	}
	src, err := file.Open()
	if err != nil {
		return fiber.ErrBadRequest
	}
	defer src.Close()
	result, err := s.UseCase.UploadMedia(ctx.UserContext(), auth.ID, ctx.Params("alertId"),
		file.Filename, file.Header.Get("Content-Type"), file.Size, src)
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[*model.AlertMediaResponse]{Data: result})
}
