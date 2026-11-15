package main

import (
	"context"
	"golang-clean-architecture/internal/config"
	"golang-clean-architecture/internal/delivery/messaging"
	"golang-clean-architecture/internal/repository"
	"golang-clean-architecture/internal/usecase"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

func main() {
	viperConfig := config.NewViper()
	logger := config.NewLogger(viperConfig)
	logger.Info("Starting worker service")

	// MySQL is read-only for the worker: it resolves push tokens and the
	// sender's emergency contacts during fan-out. Startup hard-fails when
	// the database is unreachable, like cmd/web.
	db := config.NewDatabase(viperConfig, logger)

	ctx, cancel := context.WithCancel(context.Background())

	go RunUserConsumer(logger, viperConfig, ctx)
	go RunContactConsumer(logger, viperConfig, ctx)
	go RunAddressConsumer(logger, viperConfig, ctx)
	go RunSOSConsumer(logger, viperConfig, db, ctx)

	terminateSignals := make(chan os.Signal, 1)
	signal.Notify(terminateSignals, syscall.SIGINT, syscall.SIGKILL, syscall.SIGTERM)

	stop := false
	for !stop {
		select {
		case s := <-terminateSignals:
			logger.Info("Got one of stop signals, shutting down worker gracefully, SIGNAL NAME :", s)
			cancel()
			stop = true
		}
	}

	time.Sleep(5 * time.Second) // wait for all consumers to finish processing
}

func RunAddressConsumer(logger *logrus.Logger, viperConfig *viper.Viper, ctx context.Context) {
	logger.Info("setup address consumer")
	addressConsumerGroup := config.NewKafkaConsumerGroup(viperConfig, logger)
	addressHandler := messaging.NewAddressConsumer(logger)
	messaging.ConsumeTopic(ctx, addressConsumerGroup, "addresses", logger, addressHandler.Consume)
}

func RunContactConsumer(logger *logrus.Logger, viperConfig *viper.Viper, ctx context.Context) {
	logger.Info("setup contact consumer")
	contactConsumerGroup := config.NewKafkaConsumerGroup(viperConfig, logger)
	contactHandler := messaging.NewContactConsumer(logger)
	messaging.ConsumeTopic(ctx, contactConsumerGroup, "contacts", logger, contactHandler.Consume)
}

func RunUserConsumer(logger *logrus.Logger, viperConfig *viper.Viper, ctx context.Context) {
	logger.Info("setup user consumer")
	userConsumerGroup := config.NewKafkaConsumerGroup(viperConfig, logger)
	userHandler := messaging.NewUserConsumer(logger)
	messaging.ConsumeTopic(ctx, userConsumerGroup, "users", logger, userHandler.Consume)
}

// RunSOSConsumer fans SOS events out asynchronously. The worker uses the
// same MySQL connection as cmd/web to load each recipient's FCM/APNs
// token and the sender's emergency contacts (the SMS fallback), so the
// Kafka path delivers exactly what the direct path does. FCM/APNs/Twilio
// credentials still come from config; every channel stays config-gated.
func RunSOSConsumer(logger *logrus.Logger, viperConfig *viper.Viper, db *gorm.DB, ctx context.Context) {
	logger.Info("setup sos consumer")
	notificationConfig := config.NewNotificationConfig(viperConfig, logger)
	webPushConfig := config.NewWebPushConfig(viperConfig)
	dispatcher := usecase.NewAlertDispatcher(logger,
		&usecase.FCMNotifier{Enabled: notificationConfig.FCMEnabled, ProjectID: notificationConfig.FCMProjectID, AccessToken: notificationConfig.FCMAccessToken},
		&usecase.APNsNotifier{Enabled: notificationConfig.APNsEnabled, KeyID: notificationConfig.APNsKeyID, TeamID: notificationConfig.APNsTeamID, BundleID: notificationConfig.APNsBundleID, PrivateKeyPEM: notificationConfig.APNsPrivateKey, Production: notificationConfig.APNsProduction},
		&usecase.TwilioNotifier{Enabled: notificationConfig.TwilioEnabled, AccountSID: notificationConfig.TwilioSID, AuthToken: notificationConfig.TwilioToken, FromNumber: notificationConfig.TwilioFrom},
		// Browser web push reads push_subscriptions from the same DB, so the
		// worker delivers it too; without VAPID keys it degrades to log-only
		// like the HTTP path.
		&usecase.WebPushNotifier{
			Enabled:         webPushConfig.Enabled,
			VAPIDPublicKey:  webPushConfig.VAPIDPublicKey,
			VAPIDPrivateKey: webPushConfig.VAPIDPrivateKey,
			Subscriber:      webPushConfig.Subscriber,
			TTLSeconds:      webPushConfig.TTLSeconds,
			Subs:            repository.NewPushSubscriptionRepository(db, logger),
			Log:             logger,
			AllowedHosts:    webPushConfig.AllowedHosts,
		},
		repository.NewUserReaderRepository(db, logger),
		&repository.DispatchContactRepository{DB: db, Repo: repository.NewEmergencyContactRepository(logger)},
		nil)
	sosConsumerGroup := config.NewKafkaConsumerGroup(viperConfig, logger)
	sosHandler := messaging.NewSOSConsumer(logger, dispatcher)
	messaging.ConsumeTopic(ctx, sosConsumerGroup, "sos.dispatched", logger, sosHandler.Consume)
}
