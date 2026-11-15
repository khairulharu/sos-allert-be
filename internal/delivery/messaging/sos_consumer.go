package messaging

import (
	"context"
	"encoding/json"
	"time"

	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/usecase"

	"github.com/IBM/sarama"
	"github.com/sirupsen/logrus"
)

// SosConsumeTimeout bounds the notification work for one message: a provider
// that accepts the connection and then stalls must not pin the partition (or
// the shutdown path) forever (#42). ConsumerGroupHandler retries/dead-letters
// the message when the deadline hits, exactly like any other delivery error.
const SosConsumeTimeout = 10 * time.Second

// SOSConsumer handles sos.dispatched events in the worker: it fans the
// notification out asynchronously, decoupled from the HTTP trigger.
// Events marked push_only (post-trigger escalation) skip the
// emergency-contact SMS fallback. A delivery failure returns an error;
// ConsumerGroupHandler then retries the same message in place and, when
// it keeps failing, republishes it to the dead-letter topic before the
// offset moves, so one flaky gateway never loses the alert.
type SOSConsumer struct {
	Log      *logrus.Logger
	Notifier usecase.Notifier
	// Timeout bounds one message (default SosConsumeTimeout).
	Timeout time.Duration
}

// NewSOSConsumer builds the handler.
func NewSOSConsumer(log *logrus.Logger, notifier usecase.Notifier) *SOSConsumer {
	return &SOSConsumer{Log: log, Notifier: notifier, Timeout: SosConsumeTimeout}
}

func (c *SOSConsumer) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return SosConsumeTimeout
}

// Consume processes one Kafka message.
func (c *SOSConsumer) Consume(message *sarama.ConsumerMessage) error {
	var event model.SOSEvent
	if err := json.Unmarshal(message.Value, &event); err != nil {
		c.Log.WithError(err).Error("invalid SOS event payload")
		return err
	}
	alert := model.Alert{
		ID:             event.AlertID,
		SenderId:       event.SenderID,
		Status:         event.Status,
		EmergencyType:  event.EmergencyType,
		Severity:       event.Severity,
		Summary:        event.Summary,
		ClientSeverity: event.ClientSeverity,
		ClientSummary:  event.ClientSummary,
		TriggeredAt:    time.UnixMilli(event.TriggeredAt),
		Location: model.LocationResponse{
			Latitude:  event.Latitude,
			Longitude: event.Longitude,
		},
	}
	// Bound the delivery so a hung push/SMS provider cannot block the
	// partition (and therefore the consumer-group shutdown) indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout())
	defer cancel()
	if event.PushOnly {
		// Escalation event: it carries only the responders added by a
		// wider radius, so the emergency-contact SMS fallback must not run
		// again. Prefer the push-only surface; a notifier without it still
		// delivers through the full fan-out, which is better than dropping
		// the alert.
		if rn, ok := c.Notifier.(usecase.RecipientNotifier); ok {
			if err := rn.NotifyRecipients(ctx, event.Recipients, alert); err != nil {
				c.Log.WithError(err).Error("SOS escalation dispatch from worker failed")
				return err
			}
			return nil
		}
		c.Log.Warn("notifier has no push-only surface, escalating through the full fan-out")
	}
	if err := c.Notifier.Notify(ctx, event.Recipients, alert); err != nil {
		c.Log.WithError(err).Error("SOS dispatch from worker failed")
		return err
	}
	return nil
}
