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

// SOSConsumer handles sos.dispatched events in the worker: it fans the
// notification out asynchronously, decoupled from the HTTP trigger.
// Events marked push_only (post-trigger escalation) skip the
// emergency-contact SMS fallback. A delivery failure returns an error so
// the consumer group does not mark the message (redelivery);
// poison-message/DLQ handling is a follow-up noted in the README.
type SOSConsumer struct {
	Log      *logrus.Logger
	Notifier usecase.Notifier
}

// NewSOSConsumer builds the handler.
func NewSOSConsumer(log *logrus.Logger, notifier usecase.Notifier) *SOSConsumer {
	return &SOSConsumer{Log: log, Notifier: notifier}
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
	ctx := context.Background()
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
