package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang-clean-architecture/internal/config"
	"golang-clean-architecture/internal/delivery/messaging"
	"golang-clean-architecture/internal/repository"
	"golang-clean-architecture/internal/usecase"

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

	kafkaConsumerConfig := config.NewKafkaConsumerConfig(viperConfig)
	kafkaConnectPolicy := config.NewKafkaConnectPolicy(viperConfig)
	logger.WithFields(logrus.Fields{
		"max_retries":       kafkaConsumerConfig.MaxRetries,
		"retry_backoff":     kafkaConsumerConfig.RetryBackoff,
		"dead_letter_topic": kafkaConsumerConfig.DLQTopic,
		"connect_attempts":  kafkaConnectPolicy.Attempts,
		"connect_backoff":   kafkaConnectPolicy.Backoff,
	}).Info("consumer retry and dead-letter policy")

	ctx, cancel := context.WithCancel(context.Background())

	// The entity topics are best-effort mirrors: they retry but have no
	// dead-letter topic. Only sos.dispatched carries a notification that
	// must not be lost.
	retryOptions := messaging.ConsumerOptions{
		MaxRetries:   kafkaConsumerConfig.MaxRetries,
		RetryBackoff: kafkaConsumerConfig.RetryBackoff,
	}
	sosOptions := retryOptions
	sosOptions.DeadLetter = newDeadLetter(logger, viperConfig, kafkaConsumerConfig.DLQTopic)

	go RunUserConsumer(logger, viperConfig, retryOptions, ctx)
	go RunContactConsumer(logger, viperConfig, retryOptions, ctx)
	go RunAddressConsumer(logger, viperConfig, retryOptions, ctx)
	go RunSOSConsumer(logger, viperConfig, db, sosOptions, ctx)

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

// newDeadLetter builds the sos.dispatched dead-letter publisher. Kafka's
// producer is optional (kafka.producer.enabled, off by default), so this
// returns nil when it is disabled or the topic is empty; the consumer then
// logs the drop after the retries instead of panicking.
func newDeadLetter(logger *logrus.Logger, viperConfig *viper.Viper, topic string) *messaging.DeadLetter {
	if topic == "" {
		logger.Warn("kafka.dlq.topic is empty: failed messages are dropped after their retries")
		return nil
	}
	producer := config.NewKafkaProducer(viperConfig, logger)
	if producer == nil {
		logger.Warn("Kafka producer is disabled: failed messages are dropped after their retries, no dead-letter topic")
		return nil
	}
	logger.WithField("topic", topic).Info("setup dead-letter producer")
	return messaging.NewDeadLetter(producer, topic, logger)
}

func RunAddressConsumer(logger *logrus.Logger, viperConfig *viper.Viper, options messaging.ConsumerOptions, ctx context.Context) {
	logger.Info("setup address consumer")
	addressConsumerGroup := config.NewKafkaConsumerGroup(viperConfig, logger, "addresses")
	if addressConsumerGroup == nil {
		logger.Warn("addresses consumer skipped: Kafka is unavailable")
		return
	}
	addressHandler := messaging.NewAddressConsumer(logger)
	messaging.ConsumeTopic(ctx, addressConsumerGroup, "addresses", logger, addressHandler.Consume, options)
}

func RunContactConsumer(logger *logrus.Logger, viperConfig *viper.Viper, options messaging.ConsumerOptions, ctx context.Context) {
	logger.Info("setup contact consumer")
	contactConsumerGroup := config.NewKafkaConsumerGroup(viperConfig, logger, "contacts")
	if contactConsumerGroup == nil {
		logger.Warn("contacts consumer skipped: Kafka is unavailable")
		return
	}
	contactHandler := messaging.NewContactConsumer(logger)
	messaging.ConsumeTopic(ctx, contactConsumerGroup, "contacts", logger, contactHandler.Consume, options)
}

func RunUserConsumer(logger *logrus.Logger, viperConfig *viper.Viper, options messaging.ConsumerOptions, ctx context.Context) {
	logger.Info("setup user consumer")
	userConsumerGroup := config.NewKafkaConsumerGroup(viperConfig, logger, "users")
	if userConsumerGroup == nil {
		logger.Warn("users consumer skipped: Kafka is unavailable")
		return
	}
	userHandler := messaging.NewUserConsumer(logger)
	messaging.ConsumeTopic(ctx, userConsumerGroup, "users", logger, userHandler.Consume, options)
}

// RunSOSConsumer fans SOS events out asynchronously. The worker uses the
// same MySQL connection as cmd/web to load each recipient's FCM/APNs
// token and the sender's emergency contacts (the SMS fallback), so the
// Kafka path delivers exactly what the direct path does. FCM/APNs/Twilio
// credentials still come from config; every channel stays config-gated.
// A delivery failure is retried on the same message and, when it keeps
// failing, republished to the dead-letter topic before the offset moves.
func RunSOSConsumer(logger *logrus.Logger, viperConfig *viper.Viper, db *gorm.DB, options messaging.ConsumerOptions, ctx context.Context) {
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
	sosConsumerGroup := config.NewKafkaConsumerGroup(viperConfig, logger, "sos.dispatched")
	if sosConsumerGroup == nil {
		// The web process still notifies directly (Kafka publish falls back
		// to the local dispatcher), so the worker can start without it and
		// keeps retrying on the next deploy instead of dying (#42).
		logger.Warn("sos.dispatched consumer skipped: Kafka is unavailable; the web process delivers notifications directly")
		return
	}
	sosHandler := messaging.NewSOSConsumer(logger, dispatcher)
	messaging.ConsumeTopic(ctx, sosConsumerGroup, "sos.dispatched", logger, sosHandler.Consume, options)
}
