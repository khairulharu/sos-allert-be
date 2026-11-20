package usecase

import (
	"context"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/gateway/messaging"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/model/converter"
	"golang-clean-architecture/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type ContactUseCase struct {
	DB                *gorm.DB
	Log               *logrus.Logger
	Validate          *validator.Validate
	ContactRepository *repository.ContactRepository
	ContactProducer   *messaging.ContactProducer
}

func NewContactUseCase(db *gorm.DB, logger *logrus.Logger, validate *validator.Validate,
	contactRepository *repository.ContactRepository, contactProducer *messaging.ContactProducer) *ContactUseCase {
	return &ContactUseCase{
		DB:                db,
		Log:               logger,
		Validate:          validate,
		ContactRepository: contactRepository,
		ContactProducer:   contactProducer,
	}
}

func (c *ContactUseCase) Create(ctx context.Context, request *model.CreateContactRequest) (*model.ContactResponse, error) {
	tx := c.DB.WithContext(ctx).Begin()
	defer tx.Rollback()

	if err := c.Validate.Struct(request); err != nil {
		c.Log.WithError(err).Error("error validating request body")
		return nil, fiber.ErrBadRequest
	}

	contact := &entity.Contact{
		ID:        uuid.New().String(),
		FirstName: request.FirstName,
		LastName:  request.LastName,
		Email:     request.Email,
		Phone:     request.Phone,
		UserId:    request.UserId,
	}

	if err := c.ContactRepository.Create(tx, contact); err != nil {
		c.Log.WithError(err).Error("error creating contact")
		return nil, fiber.ErrInternalServerError
	}

	if err := tx.Commit().Error; err != nil {
		c.Log.WithError(err).Error("error creating contact")
		return nil, fiber.ErrInternalServerError
	}

	if c.ContactProducer != nil {
		event := converter.ContactToEvent(contact)
		publishAfterCommit(c.Log, "contact created", func() error { return c.ContactProducer.Send(event) })
		c.Log.Info("Published contact created event")
	} else {
		c.Log.Info("Kafka producer is disabled, skipping contact created event")
	}

	return converter.ContactToResponse(contact), nil
}

func (c *ContactUseCase) Update(ctx context.Context, request *model.UpdateContactRequest) (*model.ContactResponse, error) {
	tx := c.DB.WithContext(ctx).Begin()
	defer tx.Rollback()

	contact := new(entity.Contact)
	if err := c.ContactRepository.FindByIdAndUserId(tx, contact, request.ID, request.UserId); err != nil {
		c.Log.WithError(err).Error("error getting contact")
		return nil, fiber.ErrNotFound
	}

	if err := c.Validate.Struct(request); err != nil {
		c.Log.WithError(err).Error("error validating request body")
		return nil, fiber.ErrBadRequest
	}

	contact.FirstName = request.FirstName
	contact.LastName = request.LastName
	contact.Email = request.Email
	contact.Phone = request.Phone

	if err := c.ContactRepository.Update(tx, contact); err != nil {
		c.Log.WithError(err).Error("error updating contact")
		return nil, fiber.ErrInternalServerError
	}

	if err := tx.Commit().Error; err != nil {
		c.Log.WithError(err).Error("error updating contact")
		return nil, fiber.ErrInternalServerError
	}

	if c.ContactProducer != nil {
		event := converter.ContactToEvent(contact)
		publishAfterCommit(c.Log, "contact updated", func() error { return c.ContactProducer.Send(event) })
		c.Log.Info("Published contact updated event")
	} else {
		c.Log.Info("Kafka producer is disabled, skipping contact updated event")
	}

	return converter.ContactToResponse(contact), nil
}

// Get reads one contact. Read-only, so it stays on the pool: wrapping a
// single SELECT in BEGIN/COMMIT only adds round trips (#56).
func (c *ContactUseCase) Get(ctx context.Context, request *model.GetContactRequest) (*model.ContactResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		c.Log.WithError(err).Error("error validating request body")
		return nil, fiber.ErrBadRequest
	}

	contact := new(entity.Contact)
	if err := c.ContactRepository.FindByIdAndUserId(c.DB.WithContext(ctx), contact, request.ID, request.UserId); err != nil {
		c.Log.WithError(err).Error("error getting contact")
		return nil, fiber.ErrNotFound
	}

	return converter.ContactToResponse(contact), nil
}

func (c *ContactUseCase) Delete(ctx context.Context, request *model.DeleteContactRequest) error {
	tx := c.DB.WithContext(ctx).Begin()
	defer tx.Rollback()

	if err := c.Validate.Struct(request); err != nil {
		c.Log.WithError(err).Error("error validating request body")
		return fiber.ErrBadRequest
	}

	contact := new(entity.Contact)
	if err := c.ContactRepository.FindByIdAndUserId(tx, contact, request.ID, request.UserId); err != nil {
		c.Log.WithError(err).Error("error getting contact")
		return fiber.ErrNotFound
	}

	if err := c.ContactRepository.Delete(tx, contact); err != nil {
		c.Log.WithError(err).Error("error deleting contact")
		return fiber.ErrInternalServerError
	}

	if err := tx.Commit().Error; err != nil {
		c.Log.WithError(err).Error("error deleting contact")
		return fiber.ErrInternalServerError
	}

	return nil
}

// Search pages the caller's contacts. The count and the page query are
// independent reads, so they run on the pool instead of inside a
// transaction that would only add BEGIN/COMMIT round trips (#56).
func (c *ContactUseCase) Search(ctx context.Context, request *model.SearchContactRequest) ([]model.ContactResponse, int64, error) {
	if err := c.Validate.Struct(request); err != nil {
		c.Log.WithError(err).Error("error validating request body")
		return nil, 0, fiber.ErrBadRequest
	}

	contacts, total, err := c.ContactRepository.Search(c.DB.WithContext(ctx), request)
	if err != nil {
		c.Log.WithError(err).Error("error getting contacts")
		return nil, 0, fiber.ErrInternalServerError
	}

	responses := make([]model.ContactResponse, len(contacts))
	for i, contact := range contacts {
		responses[i] = *converter.ContactToResponse(&contact)
	}

	return responses, total, nil
}
