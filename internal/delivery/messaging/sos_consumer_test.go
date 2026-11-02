package messaging

import (
	"context"
	"encoding/json"
	"testing"

	"golang-clean-architecture/internal/model"

	"github.com/IBM/sarama"
	"github.com/sirupsen/logrus"
)

type captureNotifier struct {
	recipients []string
	alert      model.Alert
	calls      int
}

func (c *captureNotifier) Notify(ctx context.Context, recipientIDs []string, alert model.Alert) error {
	c.calls++
	c.recipients = recipientIDs
	c.alert = alert
	return nil
}

func TestSOSConsumerCarriesTriageToNotifier(t *testing.T) {
	event := model.SOSEvent{
		EventType:     "sos.dispatched",
		AlertID:       "a1",
		SenderID:      "sender",
		EmergencyType: "MEDICAL",
		Status:        "TRIGGERED",
		Recipients:    []string{"user-b"},
		TriggeredAt:   1700000000000,
		Severity:      0.8,
		Summary:       "PRIORITAS TINGGI: Kemungkinan darurat medis",
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	notifier := &captureNotifier{}
	consumer := NewSOSConsumer(logrus.New(), notifier)
	if err := consumer.Consume(&sarama.ConsumerMessage{Value: payload}); err != nil {
		t.Fatalf("Consume error = %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("notifier calls = %d, want 1", notifier.calls)
	}
	if notifier.alert.Severity != event.Severity {
		t.Fatalf("alert severity = %v, want %v", notifier.alert.Severity, event.Severity)
	}
	if notifier.alert.Summary != event.Summary {
		t.Fatalf("alert summary = %q, want %q", notifier.alert.Summary, event.Summary)
	}
	if len(notifier.recipients) != 1 || notifier.recipients[0] != "user-b" {
		t.Fatalf("recipients = %v, want [user-b]", notifier.recipients)
	}
}
