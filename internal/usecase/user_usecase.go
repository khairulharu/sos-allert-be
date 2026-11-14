package usecase

import (
	"context"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/gateway/messaging"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/model/converter"
	"golang-clean-architecture/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// SessionStore keeps authenticated sessions out of MySQL so tokens can expire
// and are never stored as plaintext rows. Satisfied by
// *repository.SessionRepository.
type SessionStore interface {
	// Create stores a new session and invalidates the user's previous token
	// (single-session).
	Create(ctx context.Context, userID, token string, ttl time.Duration) error
	// Get resolves a raw token to a user id. An unknown or expired token
	// yields "" with a nil error.
	Get(ctx context.Context, token string) (string, error)
	// DeleteByUser removes the user's current session.
	DeleteByUser(ctx context.Context, userID string) error
}

type UserUseCase struct {
	DB             *gorm.DB
	Log            *logrus.Logger
	Validate       *validator.Validate
	UserRepository *repository.UserRepository
	Sessions       SessionStore
	TokenTTL       time.Duration
	UserProducer   *messaging.UserProducer
}

func NewUserUseCase(db *gorm.DB, logger *logrus.Logger, validate *validator.Validate,
	userRepository *repository.UserRepository, sessions SessionStore, tokenTTL time.Duration,
	userProducer *messaging.UserProducer) *UserUseCase {
	return &UserUseCase{
		DB:             db,
		Log:            logger,
		Validate:       validate,
		UserRepository: userRepository,
		Sessions:       sessions,
		TokenTTL:       tokenTTL,
		UserProducer:   userProducer,
	}
}

func (c *UserUseCase) Verify(ctx context.Context, request *model.VerifyUserRequest) (*model.Auth, error) {
	if err := c.Validate.Struct(request); err != nil {
		// A malformed token is an auth failure, not a bad request.
		return nil, fiber.ErrUnauthorized
	}

	userID, err := c.Sessions.Get(ctx, request.Token)
	if err != nil {
		// Fail closed, but with 503: a Redis outage must not look like a
		// deleted session and make every client drop its token.
		c.Log.Warnf("Failed read session : %+v", err)
		return nil, fiber.ErrServiceUnavailable
	}
	if userID == "" {
		return nil, fiber.ErrUnauthorized
	}

	return &model.Auth{ID: userID}, nil
}

func (c *UserUseCase) Create(ctx context.Context, request *model.RegisterUserRequest) (*model.UserResponse, error) {
	tx := c.DB.WithContext(ctx).Begin()
	defer tx.Rollback()

	err := c.Validate.Struct(request)
	if err != nil {
		c.Log.Warnf("Invalid request body : %+v", err)
		return nil, fiber.ErrBadRequest
	}

	total, err := c.UserRepository.CountById(tx, request.ID)
	if err != nil {
		c.Log.Warnf("Failed count user from database : %+v", err)
		return nil, fiber.ErrInternalServerError
	}

	if total > 0 {
		c.Log.Warnf("User already exists : %+v", err)
		return nil, fiber.ErrConflict
	}

	password, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		c.Log.Warnf("Failed to generate bcrype hash : %+v", err)
		return nil, fiber.ErrInternalServerError
	}

	user := &entity.User{
		ID:       request.ID,
		Password: string(password),
		Name:     request.Name,
	}

	if err := c.UserRepository.Create(tx, user); err != nil {
		c.Log.Warnf("Failed create user to database : %+v", err)
		return nil, fiber.ErrInternalServerError
	}

	if err := tx.Commit().Error; err != nil {
		c.Log.Warnf("Failed commit transaction : %+v", err)
		return nil, fiber.ErrInternalServerError
	}

	if c.UserProducer != nil {
		event := converter.UserToEvent(user)
		c.Log.Info("Publishing user created event")
		if err = c.UserProducer.Send(event); err != nil {
			c.Log.Warnf("Failed publish user created event : %+v", err)
			return nil, fiber.ErrInternalServerError
		}
	} else {
		c.Log.Info("Kafka producer is disabled, skipping user created event")
	}

	return converter.UserToResponse(user), nil
}

func (c *UserUseCase) Login(ctx context.Context, request *model.LoginUserRequest) (*model.UserResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		c.Log.Warnf("Invalid request body  : %+v", err)
		return nil, fiber.ErrBadRequest
	}

	user := new(entity.User)
	if err := c.UserRepository.FindById(c.DB.WithContext(ctx), user, request.ID); err != nil {
		c.Log.Warnf("Failed find user by id : %+v", err)
		return nil, fiber.ErrUnauthorized
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(request.Password)); err != nil {
		c.Log.Warnf("Failed to compare user password with bcrype hash : %+v", err)
		return nil, fiber.ErrUnauthorized
	}

	// Sessions live in Redis with a TTL; the users table is no longer
	// written. Create() also drops the previous token (single-session).
	token := uuid.New().String()
	if err := c.Sessions.Create(ctx, user.ID, token, c.TokenTTL); err != nil {
		c.Log.Warnf("Failed create session : %+v", err)
		return nil, fiber.ErrServiceUnavailable
	}

	if c.UserProducer != nil {
		event := converter.UserToEvent(user)
		c.Log.Info("Publishing user login event")
		if err := c.UserProducer.Send(event); err != nil {
			c.Log.Warnf("Failed publish user login event : %+v", err)
			return nil, fiber.ErrInternalServerError
		}
	} else {
		c.Log.Info("Kafka producer is disabled, skipping user login event")
	}

	return converter.UserToTokenResponse(token), nil
}

func (c *UserUseCase) Current(ctx context.Context, request *model.GetUserRequest) (*model.UserResponse, error) {
	tx := c.DB.WithContext(ctx).Begin()
	defer tx.Rollback()

	if err := c.Validate.Struct(request); err != nil {
		c.Log.Warnf("Invalid request body : %+v", err)
		return nil, fiber.ErrBadRequest
	}

	user := new(entity.User)
	if err := c.UserRepository.FindById(tx, user, request.ID); err != nil {
		c.Log.Warnf("Failed find user by id : %+v", err)
		return nil, fiber.ErrNotFound
	}

	if err := tx.Commit().Error; err != nil {
		c.Log.Warnf("Failed commit transaction : %+v", err)
		return nil, fiber.ErrInternalServerError
	}

	return converter.UserToResponse(user), nil
}

func (c *UserUseCase) Logout(ctx context.Context, request *model.LogoutUserRequest) (bool, error) {
	if err := c.Validate.Struct(request); err != nil {
		c.Log.Warnf("Invalid request body : %+v", err)
		return false, fiber.ErrBadRequest
	}

	// Delete the session first: logout must succeed even if the user row is
	// gone. The row is only needed for the Kafka event payload.
	if err := c.Sessions.DeleteByUser(ctx, request.ID); err != nil {
		c.Log.Warnf("Failed delete session : %+v", err)
		return false, fiber.ErrServiceUnavailable
	}

	if c.UserProducer != nil {
		// Payload only. A missing row (or a DB error) must not fail a
		// logout that already invalidated the session.
		user := new(entity.User)
		if err := c.UserRepository.FindById(c.DB.WithContext(ctx), user, request.ID); err != nil {
			c.Log.Warnf("Failed find user for logout event : %+v", err)
		} else {
			event := converter.UserToEvent(user)
			c.Log.Info("Publishing user logout event")
			if err := c.UserProducer.Send(event); err != nil {
				c.Log.Warnf("Failed publish user logout event : %+v", err)
				return false, fiber.ErrInternalServerError
			}
		}
	} else {
		c.Log.Info("Kafka producer is disabled, skipping user logout event")
	}

	return true, nil
}

func (c *UserUseCase) Update(ctx context.Context, request *model.UpdateUserRequest) (*model.UserResponse, error) {
	tx := c.DB.WithContext(ctx).Begin()
	defer tx.Rollback()

	if err := c.Validate.Struct(request); err != nil {
		c.Log.Warnf("Invalid request body : %+v", err)
		return nil, fiber.ErrBadRequest
	}

	user := new(entity.User)
	if err := c.UserRepository.FindById(tx, user, request.ID); err != nil {
		c.Log.Warnf("Failed find user by id : %+v", err)
		return nil, fiber.ErrNotFound
	}

	if request.Name != "" {
		user.Name = request.Name
	}

	if request.Password != "" {
		password, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
		if err != nil {
			c.Log.Warnf("Failed to generate bcrype hash : %+v", err)
			return nil, fiber.ErrInternalServerError
		}
		user.Password = string(password)
	}

	// Safe PIN: stored only as a bcrypt hash, never in plain text.
	// Set it via PATCH /api/users/_current with {"safe_pin": "1234"}.
	if request.SafePIN != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(request.SafePIN), bcrypt.DefaultCost)
		if err != nil {
			c.Log.Warnf("Failed to hash safe PIN : %+v", err)
			return nil, fiber.ErrInternalServerError
		}
		pinHash := string(hash)
		user.SafePINHash = &pinHash
	}

	if request.FCMToken != "" {
		user.FCMToken = &request.FCMToken
	}
	if request.APNsToken != "" {
		user.APNsToken = &request.APNsToken
	}

	if err := c.UserRepository.Update(tx, user); err != nil {
		c.Log.Warnf("Failed save user : %+v", err)
		return nil, fiber.ErrInternalServerError
	}

	if err := tx.Commit().Error; err != nil {
		c.Log.Warnf("Failed commit transaction : %+v", err)
		return nil, fiber.ErrInternalServerError
	}

	if c.UserProducer != nil {
		event := converter.UserToEvent(user)
		c.Log.Info("Publishing user updated event")
		if err := c.UserProducer.Send(event); err != nil {
			c.Log.Warnf("Failed publish user updated event : %+v", err)
			return nil, fiber.ErrInternalServerError
		}
	} else {
		c.Log.Info("Kafka producer is disabled, skipping user updated event")
	}

	return converter.UserToResponse(user), nil
}
