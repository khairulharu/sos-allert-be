package messaging

import (
	"context"
	"strconv"
	"time"

	"github.com/IBM/sarama"
	"github.com/sirupsen/logrus"
)

const (
	// DefaultMaxRetries is how many times a failed message is retried
	// before it is handed to the dead-letter topic.
	DefaultMaxRetries = 3
	// DefaultRetryBackoff is the wait between two attempts of the same
	// message.
	DefaultRetryBackoff = time.Second

	// dlqHeaderPrefix keeps the dead-letter metadata apart from the
	// headers the original message already carried.
	dlqHeaderPrefix = "dlq."
)

type ConsumerHandler func(message *sarama.ConsumerMessage) error

// DeadLetterPublisher is the single method of sarama.SyncProducer that the
// dead-letter path needs, kept narrow so tests can fake it.
type DeadLetterPublisher interface {
	SendMessage(msg *sarama.ProducerMessage) (partition int32, offset int64, err error)
}

// DeadLetter republishes a message whose handler kept failing to a
// dead-letter topic. The original payload and key travel byte-for-byte;
// only headers are added (source topic/partition/offset and the last
// error), so the record stays replayable.
type DeadLetter struct {
	Producer DeadLetterPublisher
	Topic    string
	Log      *logrus.Logger
}

// NewDeadLetter builds the dead-letter publisher. A nil producer or an
// empty topic leaves it disabled: consumers then log the drop instead of
// dead-lettering (see ConsumerGroupHandler).
func NewDeadLetter(producer DeadLetterPublisher, topic string, log *logrus.Logger) *DeadLetter {
	return &DeadLetter{Producer: producer, Topic: topic, Log: log}
}

func (d *DeadLetter) enabled() bool {
	return d != nil && d.Producer != nil && d.Topic != ""
}

// Publish sends one failed message to the dead-letter topic. It returns
// an error when the record could not be written, which the consumer uses
// to leave the source offset uncommitted.
func (d *DeadLetter) Publish(message *sarama.ConsumerMessage, cause error) error {
	headers := make([]sarama.RecordHeader, 0, len(message.Headers)+4)
	for _, header := range message.Headers {
		if header == nil {
			continue
		}
		headers = append(headers, sarama.RecordHeader{Key: header.Key, Value: copyBytes(header.Value)})
	}
	headers = append(headers,
		sarama.RecordHeader{Key: []byte(dlqHeaderPrefix + "source.topic"), Value: []byte(message.Topic)},
		sarama.RecordHeader{Key: []byte(dlqHeaderPrefix + "source.partition"), Value: []byte(strconv.FormatInt(int64(message.Partition), 10))},
		sarama.RecordHeader{Key: []byte(dlqHeaderPrefix + "source.offset"), Value: []byte(strconv.FormatInt(message.Offset, 10))},
		sarama.RecordHeader{Key: []byte(dlqHeaderPrefix + "error"), Value: []byte(errorText(cause))},
	)

	dlqMessage := &sarama.ProducerMessage{
		Topic:     d.Topic,
		Timestamp: time.Now(),
		Headers:   headers,
		Value:     sarama.ByteEncoder(copyBytes(message.Value)),
	}
	if len(message.Key) > 0 {
		// Keep the partition key so a replay spreads the same way.
		dlqMessage.Key = sarama.ByteEncoder(copyBytes(message.Key))
	}

	partition, offset, err := d.Producer.SendMessage(dlqMessage)
	if err != nil {
		return err
	}
	if d.Log != nil {
		d.Log.WithFields(logContext(message)).WithFields(logrus.Fields{
			"dlq_topic":     d.Topic,
			"dlq_partition": partition,
			"dlq_offset":    offset,
		}).Warn("Message moved to the dead-letter topic")
	}
	return nil
}

// ConsumerOptions configures retry and dead-letter behaviour of a topic
// consumer. The zero value is safe: it retries DefaultMaxRetries times
// with DefaultRetryBackoff and drops (with a log line) when no DeadLetter
// is configured.
type ConsumerOptions struct {
	// MaxRetries is the number of retries after the first failed attempt.
	// Zero falls back to DefaultMaxRetries; a negative value disables
	// retries so a failing message goes straight to the dead-letter topic.
	MaxRetries int
	// RetryBackoff is the wait before each retry. Zero falls back to
	// DefaultRetryBackoff; a negative value retries immediately.
	RetryBackoff time.Duration
	// DeadLetter is optional. Without it a message that exhausted its
	// retries is logged and dropped instead of dead-lettered.
	DeadLetter *DeadLetter
}

func (o ConsumerOptions) retries() int {
	switch {
	case o.MaxRetries < 0:
		return 0
	case o.MaxRetries == 0:
		return DefaultMaxRetries
	default:
		return o.MaxRetries
	}
}

func (o ConsumerOptions) backoff() time.Duration {
	switch {
	case o.RetryBackoff < 0:
		return 0
	case o.RetryBackoff == 0:
		return DefaultRetryBackoff
	default:
		return o.RetryBackoff
	}
}

type ConsumerGroupHandler struct {
	Handler ConsumerHandler
	Log     *logrus.Logger
	Options ConsumerOptions
}

func (h *ConsumerGroupHandler) Setup(sarama.ConsumerGroupSession) error {
	return nil
}

func (h *ConsumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}

func (h *ConsumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case message := <-claim.Messages():
			if message == nil {
				return nil
			}

			if err := h.handleMessage(session, message); err != nil {
				// The dead-letter write failed, so the offset stays
				// uncommitted. Stop the claim instead of moving on: a later
				// MarkMessage would commit past this message and lose it.
				return err
			}

		case <-session.Context().Done():
			return nil
		}
	}
}

// handleMessage runs the handler with retries. It returns nil when the
// message was marked (processed or dead-lettered) and an error when it
// must be redelivered.
func (h *ConsumerGroupHandler) handleMessage(session sarama.ConsumerGroupSession, message *sarama.ConsumerMessage) error {
	options := h.Options
	attempts := options.retries() + 1

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if !waitForRetry(session.Context(), options.backoff()) {
				// Shutting down mid-retry: leave the offset uncommitted so
				// the message is redelivered to the next session.
				h.Log.WithFields(logContext(message)).Warn("Consumer stopping before retry finished; message left uncommitted")
				return nil
			}
			h.Log.WithError(lastErr).WithFields(attemptFields(message, attempt, attempts)).Warn("Retrying message")
		}

		lastErr = h.Handler(message)
		if lastErr == nil {
			session.MarkMessage(message, "")
			return nil
		}

		h.Log.WithError(lastErr).WithFields(attemptFields(message, attempt, attempts)).Error("Failed to process message")
	}

	if !options.DeadLetter.enabled() {
		h.Log.WithError(lastErr).WithFields(logContext(message)).Error("No dead-letter topic configured; dropping message after retries")
		session.MarkMessage(message, "")
		return nil
	}

	if err := options.DeadLetter.Publish(message, lastErr); err != nil {
		h.Log.WithError(err).WithFields(logContext(message)).Error("Failed to publish message to the dead-letter topic; leaving offset uncommitted for redelivery")
		return err
	}

	session.MarkMessage(message, "")
	return nil
}

// waitForRetry sleeps for the backoff and reports whether the session is
// still alive. It returns false when the session context is done, so a
// shutdown never stalls on a retry wait.
func waitForRetry(ctx context.Context, backoff time.Duration) bool {
	if backoff <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(backoff)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func logContext(message *sarama.ConsumerMessage) logrus.Fields {
	return logrus.Fields{
		"topic":     message.Topic,
		"partition": message.Partition,
		"offset":    message.Offset,
	}
}

func attemptFields(message *sarama.ConsumerMessage, attempt, attempts int) logrus.Fields {
	fields := logContext(message)
	fields["attempt"] = attempt
	fields["max_attempts"] = attempts
	return fields
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func copyBytes(in []byte) []byte {
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// ConsumeTopic runs one topic consumer. Shutdown happens through ctx; a
// claim that cannot commit (dead-letter write failed) is restarted by the
// sarama consumer group, which replays from the last committed offset.
func ConsumeTopic(ctx context.Context, consumerGroup sarama.ConsumerGroup, topic string, log *logrus.Logger, handler ConsumerHandler, options ConsumerOptions) {
	consumerHandler := &ConsumerGroupHandler{
		Handler: handler,
		Log:     log,
		Options: options,
	}

	go func() {
		for {
			if err := consumerGroup.Consume(ctx, []string{topic}, consumerHandler); err != nil {
				log.WithError(err).Error("Error from consumer")
			}

			if ctx.Err() != nil {
				log.Info("Context cancelled, stopping consumer")
				return
			}
		}
	}()

	go func() {
		for err := range consumerGroup.Errors() {
			log.WithError(err).Error("Consumer group error")
		}
	}()

	<-ctx.Done()
	log.Infof("Closing consumer group for topic: %s", topic)
	if err := consumerGroup.Close(); err != nil {
		log.WithError(err).Error("Error closing consumer group")
	}
}
