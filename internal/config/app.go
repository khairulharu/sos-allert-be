package config

import (
	"golang-clean-architecture/internal/delivery/http"
	"golang-clean-architecture/internal/delivery/http/middleware"
	"golang-clean-architecture/internal/delivery/http/route"
	"golang-clean-architecture/internal/gateway/messaging"
	"golang-clean-architecture/internal/metrics"
	"golang-clean-architecture/internal/repository"
	"golang-clean-architecture/internal/storage"
	"golang-clean-architecture/internal/tracking"
	"golang-clean-architecture/internal/usecase"

	"github.com/IBM/sarama"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

type BootstrapConfig struct {
	DB          *gorm.DB
	App         *fiber.App
	Log         *logrus.Logger
	Validate    *validator.Validate
	Config      *viper.Viper
	Producer    sarama.SyncProducer
	RedisClient *redis.Client
}

func Bootstrap(config *BootstrapConfig) {
	// setup repositories
	userRepository := repository.NewUserRepository(config.Log)
	contactRepository := repository.NewContactRepository(config.Log)
	addressRepository := repository.NewAddressRepository(config.Log)
	locationCacheRepository := repository.NewLocationCacheRepository(config.RedisClient, config.Log)
	alertRepository := repository.NewAllertRepository(config.Log)
	emergencyContactRepository := repository.NewEmergencyContactRepository(config.Log)
	userReaderRepository := repository.NewUserReaderRepository(config.DB, config.Log)
	dispatchContacts := &repository.DispatchContactRepository{DB: config.DB, Repo: emergencyContactRepository}
	idempotencyRepository := repository.NewIdempotencyRepository(config.RedisClient)
	rateLimitRepository := repository.NewRateLimitRepository(config.RedisClient)

	// setup producer
	var userProducer *messaging.UserProducer
	var contactProducer *messaging.ContactProducer
	var addressProducer *messaging.AddressProducer
	var sosProducer *messaging.SOSProducer
	var sosPublisher usecase.EventPublisher

	if config.Producer != nil {
		userProducer = messaging.NewUserProducer(config.Producer, config.Log)
		contactProducer = messaging.NewContactProducer(config.Producer, config.Log)
		addressProducer = messaging.NewAddressProducer(config.Producer, config.Log)
		sosProducer = messaging.NewSOSProducer(config.Producer, "sos.dispatched", config.Log)
		sosPublisher = sosProducer
	}

	// observability + live tracking
	appMetrics := metrics.New()
	trackHub := tracking.NewHub()
	sosConfig := NewSOSConfig(config.Config)
	notificationConfig := NewNotificationConfig(config.Config, config.Log)
	storageConfig := NewStorageConfig(config.Config)

	// multi-channel dispatcher: FCM / APNs / Twilio, config-gated.
	// Without credentials it degrades to the previous log-only behaviour.
	fcmNotifier := &usecase.FCMNotifier{
		Enabled: notificationConfig.FCMEnabled, ProjectID: notificationConfig.FCMProjectID,
		AccessToken: notificationConfig.FCMAccessToken,
	}
	apnsNotifier := &usecase.APNsNotifier{
		Enabled: notificationConfig.APNsEnabled, KeyID: notificationConfig.APNsKeyID,
		TeamID: notificationConfig.APNsTeamID, BundleID: notificationConfig.APNsBundleID,
		PrivateKeyPEM: notificationConfig.APNsPrivateKey, Production: notificationConfig.APNsProduction,
	}
	twilioNotifier := &usecase.TwilioNotifier{
		Enabled: notificationConfig.TwilioEnabled, AccountSID: notificationConfig.TwilioSID,
		AuthToken: notificationConfig.TwilioToken, FromNumber: notificationConfig.TwilioFrom,
	}
	dispatcher := usecase.NewAlertDispatcher(config.Log, fcmNotifier, apnsNotifier, twilioNotifier,
		userReaderRepository, dispatchContacts, appMetrics)

	// media storage: local disk by default; S3 is not wired yet, so a
	// non-local backend disables uploads with a clear error.
	var mediaStore usecase.MediaStorage
	if storageConfig.Backend == "local" {
		mediaStore = storage.NewLocalStorage(storageConfig.LocalDir)
	} else {
		config.Log.Warnf("storage backend %q is not supported yet, media upload disabled", storageConfig.Backend)
	}

	// setup use cases
	userUseCase := usecase.NewUserUseCase(config.DB, config.Log, config.Validate, userRepository, userProducer)
	contactUseCase := usecase.NewContactUseCase(config.DB, config.Log, config.Validate, contactRepository, contactProducer)
	addressUseCase := usecase.NewAddressUseCase(config.DB, config.Log, config.Validate, contactRepository, addressRepository, addressProducer)
	locationUseCase := usecase.NewLocationUseCase(locationCacheRepository, config.Validate, config.Log)
	triggerSOSUseCase := usecase.NewTriggerSOSUseCase(config.DB, config.Log, usecase.TriggerSOSDeps{
		Validate:          config.Validate,
		Location:          locationCacheRepository,
		Alerts:            alertRepository,
		Notifier:          dispatcher,
		Idempotency:       idempotencyRepository,
		RateLimiter:       rateLimitRepository,
		Publisher:         sosPublisher,
		Users:             userReaderRepository,
		Hub:               trackHub,
		Metrics:           appMetrics,
		EmergencyContacts: emergencyContactRepository,
		MediaStore:        mediaStore,
		GracePeriod:       sosConfig.GracePeriod,
		MaxRecipients:     sosConfig.MaxRecipients,
		MinRecipients:     sosConfig.MinRecipients,
		Radii:             sosConfig.AdaptiveRadiiM,
		RateLimitPerHour:  sosConfig.RateLimitPerHour,
		RateLimitWindow:   sosConfig.RateLimitWindow,
		ExpireAfter:       sosConfig.ExpireAfter,
	})

	// setup controller
	userController := http.NewUserController(userUseCase, config.Log)
	contactController := http.NewContactController(contactUseCase, config.Log)
	addressController := http.NewAddressController(addressUseCase, config.Log)
	locationController := http.NewLocationController(locationUseCase, config.Log)
	sosController := http.NewSOSController(triggerSOSUseCase, config.Log)
	emergencyContactController := http.NewEmergencyContactController(triggerSOSUseCase, config.Log)
	sosTrackController := http.NewSOSTrackController(triggerSOSUseCase, userUseCase, trackHub, config.Log)

	// setup middleware
	authMiddleware := middleware.NewAuth(userUseCase)

	routeConfig := route.RouteConfig{
		App:                        config.App,
		UserController:             userController,
		ContactController:          contactController,
		AddressController:          addressController,
		AuthMiddleware:             authMiddleware,
		LocationController:         locationController,
		SOSController:              sosController,
		EmergencyContactController: emergencyContactController,
		SOSTrackController:         sosTrackController,
		MetricsHandler:             appMetrics.Handler(),
	}
	routeConfig.Setup()
}
