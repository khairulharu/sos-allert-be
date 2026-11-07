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
	locationRepository := repository.NewLocationRepository(config.DB, config.Log)
	alertRepository := repository.NewAllertRepository(config.Log)
	emergencyContactRepository := repository.NewEmergencyContactRepository(config.Log)
	userReaderRepository := repository.NewUserReaderRepository(config.DB, config.Log)
	pushSubscriptionRepository := repository.NewPushSubscriptionRepository(config.DB, config.Log)
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
	authConfig := NewAuthConfig(config.Config)
	notificationConfig := NewNotificationConfig(config.Config, config.Log)
	webPushConfig := NewWebPushConfig(config.Config)
	storageConfig := NewStorageConfig(config.Config)
	aiConfig := NewAIConfig(config.Config)
	sttConfig := NewSTTConfig(config.Config)

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
	// browser Web Push (VAPID): reaches signed-in browsers without FCM
	// credentials; disabled when the key pair is missing.
	webPushNotifier := &usecase.WebPushNotifier{
		Enabled:         webPushConfig.Enabled,
		VAPIDPublicKey:  webPushConfig.VAPIDPublicKey,
		VAPIDPrivateKey: webPushConfig.VAPIDPrivateKey,
		Subscriber:      webPushConfig.Subscriber,
		TTLSeconds:      webPushConfig.TTLSeconds,
		Subs:            pushSubscriptionRepository,
		Log:             config.Log,
	}
	dispatcher := usecase.NewAlertDispatcher(config.Log, fcmNotifier, apnsNotifier, twilioNotifier,
		webPushNotifier, userReaderRepository, dispatchContacts, appMetrics)

	// media storage: local disk by default; S3 is not wired yet, so a
	// non-local backend disables uploads with a clear error.
	var mediaStore usecase.MediaStorage
	if storageConfig.Backend == "local" {
		mediaStore = storage.NewLocalStorage(storageConfig.LocalDir)
	} else {
		config.Log.Warnf("storage backend %q is not supported yet, media upload disabled", storageConfig.Backend)
	}

	// Optional AI triage: offline keyword classifier by default; a live
	// Ollama/OpenAI provider takes over when configured. Provider
	// errors fall back to the offline classifier, so the SOS trigger
	// never depends on AI being up.
	var triageEngine usecase.EmergencyTriage
	if aiConfig.Enabled {
		var llmEngine usecase.EmergencyTriage
		switch aiConfig.Provider {
		case AITriageProviderOllama:
			llmEngine = usecase.NewOllamaClient(aiConfig.OllamaBaseURL, aiConfig.OllamaModel, aiConfig.Timeout)
		case AITriageProviderOpenAI:
			if aiConfig.OpenAIAPIKey == "" {
				config.Log.Warn("ai.provider=openai but ai.openai.api_key is empty; using offline classifier")
			} else {
				llmEngine = usecase.NewOpenAIClient(aiConfig.OpenAIBaseURL, aiConfig.OpenAIAPIKey, aiConfig.OpenAIModel, aiConfig.Timeout)
			}
		}
		triageEngine = usecase.NewAITriage(config.Log, llmEngine, usecase.NewOfflineClassifier())
		config.Log.Infof("AI triage enabled, provider=%q (live LLM=%v, offline fallback=on)", aiConfig.Provider, llmEngine != nil)
	}

	// Optional speech-to-text for post-trigger audio evidence. Groq is
	// the default OpenAI-compatible provider; a missing key disables
	// transcription without disabling media upload.
	var transcriber usecase.Transcriber
	if sttConfig.Enabled {
		switch {
		case sttConfig.BaseURL == "":
			config.Log.Warn("STT enabled but ai.stt.base_url is empty; audio transcription disabled")
		case sttConfig.Provider != "local" && sttConfig.APIKey == "":
			config.Log.Warn("STT enabled but ai.stt.api_key is empty; audio transcription disabled")
		default:
			transcriber = usecase.NewWhisperTranscriber(sttConfig.BaseURL, sttConfig.APIKey, sttConfig.Model, sttConfig.Language, sttConfig.Timeout)
			config.Log.Infof("STT enabled provider=%q model=%q language=%q", sttConfig.Provider, sttConfig.Model, sttConfig.Language)
		}
	}

	// setup use cases
	userUseCase := usecase.NewUserUseCase(config.DB, config.Log, config.Validate, userRepository, userProducer)
	contactUseCase := usecase.NewContactUseCase(config.DB, config.Log, config.Validate, contactRepository, contactProducer)
	addressUseCase := usecase.NewAddressUseCase(config.DB, config.Log, config.Validate, contactRepository, addressRepository, addressProducer)
	locationUseCase := usecase.NewLocationUseCase(locationCacheRepository, locationRepository, config.Validate, config.Log)
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
		Triage:            triageEngine,
		EmergencyContacts: emergencyContactRepository,
		MediaStore:        mediaStore,
		Transcriber:       transcriber,
		GracePeriod:       sosConfig.GracePeriod,
		MaxRecipients:     sosConfig.MaxRecipients,
		MinRecipients:     sosConfig.MinRecipients,
		Radii:             sosConfig.AdaptiveRadiiM,
		RateLimitPerHour:  sosConfig.RateLimitPerHour,
		RateLimitWindow:   sosConfig.RateLimitWindow,
		RateLimitDisabled: sosConfig.RateLimitDisabled,
		ExpireAfter:       sosConfig.ExpireAfter,
	})

	// setup controller
	userController := http.NewUserController(userUseCase, config.Log)
	contactController := http.NewContactController(contactUseCase, config.Log)
	addressController := http.NewAddressController(addressUseCase, config.Log)
	locationController := http.NewLocationController(locationUseCase, config.Log)
	sosController := http.NewSOSController(triggerSOSUseCase, config.Log)
	emergencyContactController := http.NewEmergencyContactController(triggerSOSUseCase, config.Log)
	pushUseCase := usecase.NewPushUseCase(config.DB, config.Log, config.Validate, pushSubscriptionRepository,
		webPushConfig.VAPIDPublicKey, webPushConfig.Enabled)
	pushController := http.NewPushController(pushUseCase, config.Log)
	sosTrackController := http.NewSOSTrackController(triggerSOSUseCase, userUseCase, trackHub, config.Log)

	// setup middleware
	authMiddleware := middleware.NewAuth(userUseCase)
	authRateLimit := middleware.NewAuthRateLimit(rateLimitRepository, config.Log,
		authConfig.RateLimitPerMinute, authConfig.Window, authConfig.Lockout)

	routeConfig := route.RouteConfig{
		App:                        config.App,
		UserController:             userController,
		ContactController:          contactController,
		AddressController:          addressController,
		AuthMiddleware:             authMiddleware,
		AuthRateLimit:              authRateLimit,
		LocationController:         locationController,
		SOSController:              sosController,
		EmergencyContactController: emergencyContactController,
		PushController:             pushController,
		SOSTrackController:         sosTrackController,
		MetricsHandler:             appMetrics.Handler(),
	}
	routeConfig.Setup()
}
