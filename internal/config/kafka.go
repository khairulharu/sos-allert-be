package config

import (
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// Kafka defaults for the consumer retry and dead-letter settings. They
// mirror messaging.DefaultMaxRetries / DefaultRetryBackoff; the config
// layer owns them so a missing key never disables retries by accident.
const (
	DefaultKafkaConsumerMaxRetries   = 3
	DefaultKafkaConsumerRetryBackoff = time.Second
	DefaultKafkaDLQTopic             = "sos.dispatched.dlq"
)

func NewKafkaConsumerGroup(config *viper.Viper, log *logrus.Logger) sarama.ConsumerGroup {
	saramaConfig := sarama.NewConfig()
	saramaConfig.Consumer.Return.Errors = true

	offsetReset := config.GetString("kafka.auto.offset.reset")
	if offsetReset == "earliest" {
		saramaConfig.Consumer.Offsets.Initial = sarama.OffsetOldest
	} else {
		saramaConfig.Consumer.Offsets.Initial = sarama.OffsetNewest
	}

	brokers := strings.Split(config.GetString("kafka.bootstrap.servers"), ",")
	groupID := config.GetString("kafka.group.id")

	consumerGroup, err := sarama.NewConsumerGroup(brokers, groupID, saramaConfig)
	if err != nil {
		log.Fatalf("Failed to create consumer group: %v", err)
	}
	return consumerGroup
}

// KafkaConsumerConfig carries the retry and dead-letter settings of the
// worker's topic consumers.
type KafkaConsumerConfig struct {
	// MaxRetries is the number of retries after the first failed attempt.
	// Negative disables retries (a failing message goes straight to the
	// dead-letter topic); 0 means "unset" and keeps the default.
	MaxRetries int
	// RetryBackoff is the wait before each retry.
	RetryBackoff time.Duration
	// DLQTopic is the dead-letter topic. An explicitly empty value
	// disables dead-lettering, so messages are logged and dropped after
	// their retries; an absent key uses DefaultKafkaDLQTopic.
	DLQTopic string
}

// NewKafkaConsumerConfig reads `kafka.consumer.max_retries`,
// `kafka.consumer.retry_backoff_seconds` and `kafka.dlq.topic`. Missing
// keys fall back to the safe defaults above.
func NewKafkaConsumerConfig(config *viper.Viper) *KafkaConsumerConfig {
	cfg := &KafkaConsumerConfig{
		MaxRetries:   DefaultKafkaConsumerMaxRetries,
		RetryBackoff: DefaultKafkaConsumerRetryBackoff,
		DLQTopic:     DefaultKafkaDLQTopic,
	}

	if config.IsSet("kafka.consumer.max_retries") {
		cfg.MaxRetries = config.GetInt("kafka.consumer.max_retries")
	}
	if config.IsSet("kafka.consumer.retry_backoff_seconds") {
		cfg.RetryBackoff = time.Duration(config.GetInt("kafka.consumer.retry_backoff_seconds")) * time.Second
	}
	if config.IsSet("kafka.dlq.topic") {
		cfg.DLQTopic = strings.TrimSpace(config.GetString("kafka.dlq.topic"))
	}
	return cfg
}

func NewKafkaProducer(config *viper.Viper, log *logrus.Logger) sarama.SyncProducer {
	if !config.GetBool("kafka.producer.enabled") {
		log.Info("Kafka producer is disabled")
		return nil
	}

	saramaConfig := sarama.NewConfig()
	saramaConfig.Producer.Return.Successes = true
	saramaConfig.Producer.RequiredAcks = sarama.WaitForAll
	saramaConfig.Producer.Retry.Max = 3

	brokers := strings.Split(config.GetString("kafka.bootstrap.servers"), ",")

	producer, err := sarama.NewSyncProducer(brokers, saramaConfig)
	if err != nil {
		log.Fatalf("Failed to create producer: %v", err)
	}
	return producer
}
