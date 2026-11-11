package http

import (
	"errors"
	"math"
	"path/filepath"
	"strconv"

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
	case errors.Is(err, model.ErrAlertNotFound),
		errors.Is(err, model.ErrMediaNotFound):
		return fiber.ErrNotFound
	case errors.Is(err, model.ErrInvalidTransition):
		return fiber.ErrConflict
	case errors.Is(err, model.ErrInvalidPIN):
		return fiber.ErrUnauthorized
	case errors.Is(err, model.ErrPINNotConfigured),
		errors.Is(err, model.ErrStorageNotConfigured):
		return fiber.NewError(fiber.StatusPreconditionFailed, err.Error())
	case errors.Is(err, model.ErrRateLimited):
		return fiber.NewError(fiber.StatusTooManyRequests, err.Error())
	case errors.Is(err, model.ErrNotAlertOwner),
		errors.Is(err, model.ErrNotAlertParticipant):
		return fiber.ErrForbidden
	case errors.Is(err, model.ErrTranscriptionNotConfigured):
		return fiber.NewError(fiber.StatusServiceUnavailable, err.Error())
	case errors.Is(err, model.ErrTranscriptionFailed):
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
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
		Triage:             result.Triage,
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

// Classify transcribes and classifies a short audio clip before the SOS
// trigger, so the first fan-out can already use the audio-derived level.
// Nothing is persisted: the client passes the result into POST /api/sos.
func (s *SOSController) Classify(ctx *fiber.Ctx) error {
	file, err := ctx.FormFile("file")
	if err != nil {
		return fiber.ErrBadRequest
	}
	src, err := file.Open()
	if err != nil {
		return fiber.ErrBadRequest
	}
	defer src.Close()
	result, err := s.UseCase.ClassifyAudio(ctx.UserContext(), file.Filename,
		file.Header.Get("Content-Type"), file.Size, src)
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[*model.ClassifyResponse]{Data: result})
}

func (s *SOSController) ListInbox(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	alerts, err := s.UseCase.ListInbox(ctx.UserContext(), auth.ID)
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[[]*model.AlertDetailResponse]{Data: alerts})
}

// ListHistory serves GET /api/sos/history: one page of the caller's sent
// or received incidents, every status, newest first.
func (s *SOSController) ListHistory(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)

	request := &model.SearchAlertHistoryRequest{
		UserID: auth.ID,
		Role:   ctx.Query("role", "sent"),
		Status: ctx.Query("status", ""),
		Page:   ctx.QueryInt("page", 1),
		Size:   ctx.QueryInt("size", 20),
	}
	if raw := ctx.Query("from_ms"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fiber.ErrBadRequest
		}
		request.FromMs = &v
	}
	if raw := ctx.Query("to_ms"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fiber.ErrBadRequest
		}
		request.ToMs = &v
	}

	responses, total, err := s.UseCase.ListHistory(ctx.UserContext(), request)
	if err != nil {
		s.Log.WithError(err).Error("error listing alert history")
		return mapSOSError(err)
	}

	paging := &model.PageMetadata{
		Page:      request.Page,
		Size:      request.Size,
		TotalItem: total,
		TotalPage: int64(math.Ceil(float64(total) / float64(request.Size))),
	}
	return ctx.JSON(model.WebResponse[[]*model.AlertDetailResponse]{Data: responses, Paging: paging})
}

// Summary serves GET /api/sos/summary: the aggregate counters behind the
// alert list cards.
func (s *SOSController) Summary(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	result, err := s.UseCase.Summary(ctx.UserContext(), auth.ID)
	if err != nil {
		s.Log.WithError(err).Error("error loading alert summary")
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[*model.AlertSummaryResponse]{Data: result})
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
	auth := middleware.GetUser(ctx)
	detail, err := s.UseCase.Acknowledge(ctx.UserContext(), auth.ID, ctx.Params("alertId"))
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

func (s *SOSController) ListMedia(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	media, err := s.UseCase.ListAlertMedia(ctx.UserContext(), auth.ID, ctx.Params("alertId"))
	if err != nil {
		return mapSOSError(err)
	}
	return ctx.JSON(model.WebResponse[[]*model.AlertMediaResponse]{Data: media})
}

// DownloadMedia streams one upload. The raw session token goes in the
// Authorization header like every other HTTP endpoint; players that
// cannot set headers should download the file first.
func (s *SOSController) DownloadMedia(ctx *fiber.Ctx) error {
	auth := middleware.GetUser(ctx)
	media, reader, err := s.UseCase.OpenAlertMedia(ctx.UserContext(), auth.ID,
		ctx.Params("alertId"), ctx.Params("mediaId"))
	if err != nil {
		return mapSOSError(err)
	}
	ctx.Set(fiber.HeaderContentType, media.ContentType)
	ctx.Set(fiber.HeaderContentDisposition, "inline; filename="+strconv.Quote(filepath.Base(media.Filename)))
	ctx.Set(fiber.HeaderCacheControl, "private, max-age=300")
	// fasthttp closes the reader after streaming it; do not defer Close here.
	return ctx.SendStream(reader, int(media.SizeBytes))
}
