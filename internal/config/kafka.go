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

	// DefaultKafkaGroupID is the consumer-group base used when
	// kafka.group.id is unset. An empty group id makes sarama refuse to
	// start, which would silently disable every consumer.
	DefaultKafkaGroupID = "sos-allert"
	// DefaultKafkaConnectAttempts / DefaultKafkaConnectBackoff bound the
	// startup retry when a broker is unreachable.
	DefaultKafkaConnectAttempts = 3
	DefaultKafkaConnectBackoff  = 2 * time.Second
)

// kafkaGroupSuffixes maps each consumed topic to its own consumer-group
// suffix. Per-topic groups keep a rebalance (or a stalled consumer) on one
// topic from disturbing the other three (#42).
var kafkaGroupSuffixes = map[string]string{
	"users":          "-users",
	"contacts":       "-contacts",
	"addresses":      "-addresses",
	"sos.dispatched": "-sos",
}

// KafkaGroupID derives the consumer-group id of one topic from the
// configured base (kafka.group.id). An empty base falls back to
// DefaultKafkaGroupID; an unknown topic gets a sanitized suffix of its own
// so two consumers can never share a group by accident.
func KafkaGroupID(base, topic string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = DefaultKafkaGroupID
	}
	if suffix, ok := kafkaGroupSuffixes[topic]; ok {
		return base + suffix
	}
	return base + "-" + sanitizeGroupSuffix(topic)
}

func sanitizeGroupSuffix(topic string) string {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return "default"
	}
	return strings.NewReplacer(".", "-", "/", "-", " ", "-", ":", "-").Replace(topic)
}

// KafkaConnectPolicy bounds the startup connect retry. Kafka is optional:
// after these attempts the caller logs the failure and carries on without
// that client instead of killing the process (#42).
type KafkaConnectPolicy struct {
	Attempts int
	Backoff  time.Duration
}

// NewKafkaConnectPolicy reads kafka.connect.max_attempts (default 3, floor
// 1) and kafka.connect.retry_backoff_seconds (default 2 s, floor 0).
func NewKafkaConnectPolicy(config *viper.Viper) KafkaConnectPolicy {
	policy := KafkaConnectPolicy{Attempts: DefaultKafkaConnectAttempts, Backoff: DefaultKafkaConnectBackoff}
	if config.IsSet("kafka.connect.max_attempts") {
		policy.Attempts = config.GetInt("kafka.connect.max_attempts")
	}
	if policy.Attempts < 1 {
		policy.Attempts = 1
	}
	if config.IsSet("kafka.connect.retry_backoff_seconds") {
		backoff := config.GetFloat64("kafka.connect.retry_backoff_seconds")
		if backoff < 0 {
			backoff = 0
		}
		policy.Backoff = time.Duration(backoff * float64(time.Second))
	}
	return policy
}

// connectWithRetry calls connect up to policy.Attempts times, sleeping
// policy.Backoff*attempt between tries. sleep is a parameter so tests do not
// wait for real backoff.
func connectWithRetry(policy KafkaConnectPolicy, sleep func(time.Duration), connect func() error) error {
	attempts := policy.Attempts
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err = connect(); err == nil {
			return nil
		}
		if attempt < attempts && policy.Backoff > 0 {
			sleep(time.Duration(attempt) * policy.Backoff)
		}
	}
	return err
}

func kafkaBrokers(config *viper.Viper) []string {
	raw := strings.Split(config.GetString("kafka.bootstrap.servers"), ",")
	brokers := make([]string, 0, len(raw))
	for _, broker := range raw {
		if broker = strings.TrimSpace(broker); broker != "" {
			brokers = append(brokers, broker)
		}
	}
	if len(brokers) == 0 {
		brokers = []string{"localhost:9092"}
	}
	return brokers
}

// NewKafkaConsumerGroup builds the consumer group for one topic. Kafka is
// optional: when the brokers stay unreachable after the retry budget it logs
// an error and returns nil, and the caller skips that consumer instead of
// the process dying at startup (#42).
func NewKafkaConsumerGroup(config *viper.Viper, log *logrus.Logger, topic string) sarama.ConsumerGroup {
	saramaConfig := sarama.NewConfig()
	saramaConfig.Consumer.Return.Errors = true

	offsetReset := config.GetString("kafka.auto.offset.reset")
	if offsetReset == "earliest" {
		saramaConfig.Consumer.Offsets.Initial = sarama.OffsetOldest
	} else {
		saramaConfig.Consumer.Offsets.Initial = sarama.OffsetNewest
	}

	brokers := kafkaBrokers(config)
	groupID := KafkaGroupID(config.GetString("kafka.group.id"), topic)
	policy := NewKafkaConnectPolicy(config)

	var consumerGroup sarama.ConsumerGroup
	err := connectWithRetry(policy, time.Sleep, func() error {
		created, err := sarama.NewConsumerGroup(brokers, groupID, saramaConfig)
		if err != nil {
			return err
		}
		consumerGroup = created
		return nil
	})
	if err != nil {
		log.WithError(err).WithFields(logrus.Fields{
			"topic":    topic,
			"group_id": groupID,
			"attempts": policy.Attempts,
		}).Error("Kafka consumer group unavailable; this consumer is disabled and the service keeps running")
		return nil
	}
	log.WithFields(logrus.Fields{"topic": topic, "group_id": groupID}).Info("Kafka consumer group ready")
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

// NewKafkaProducer builds the sync producer. It is optional and disabled by
// default; when it is enabled but the brokers stay unreachable it logs an
// error and returns nil, and the web process keeps notifying directly (#42).
func NewKafkaProducer(config *viper.Viper, log *logrus.Logger) sarama.SyncProducer {
	if !config.GetBool("kafka.producer.enabled") {
		log.Info("Kafka producer is disabled")
		return nil
	}

	saramaConfig := sarama.NewConfig()
	saramaConfig.Producer.Return.Successes = true
	saramaConfig.Producer.RequiredAcks = sarama.WaitForAll
	saramaConfig.Producer.Retry.Max = 3

	brokers := kafkaBrokers(config)
	policy := NewKafkaConnectPolicy(config)

	var producer sarama.SyncProducer
	err := connectWithRetry(policy, time.Sleep, func() error {
		created, err := sarama.NewSyncProducer(brokers, saramaConfig)
		if err != nil {
			return err
		}
		producer = created
		return nil
	})
	if err != nil {
		log.WithError(err).WithField("attempts", policy.Attempts).
			Error("Kafka producer unavailable; events are skipped and notifications fall back to direct delivery")
		return nil
	}
	return producer
}
