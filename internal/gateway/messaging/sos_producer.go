package messaging

import (
	"context"
	"encoding/json"

	"golang-clean-architecture/internal/model"

	"github.com/IBM/sarama"
	"github.com/sirupsen/logrus"
)

// SOSProducer publishes SOS events to the sos.dispatched Kafka topic.
// A nil producer means Kafka is disabled; Send is then a no-op.
type SOSProducer struct {
	Producer sarama.SyncProducer
	Topic    string
	Log      *logrus.Logger
}

// NewSOSProducer builds a producer for the given topic.
func NewSOSProducer(producer sarama.SyncProducer, topic string, log *logrus.Logger) *SOSProducer {
	if topic == "" {
		topic = "sos.dispatched"
	}
	return &SOSProducer{Producer: producer, Topic: topic, Log: log}
}

// Send publishes one event. It is called after the alert row is
// committed, and a publish failure is logged, never fatal to the SOS.
func (p *SOSProducer) Send(ctx context.Context, event model.SOSEvent) error {
	if p.Producer == nil {
		return nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	msg := &sarama.ProducerMessage{
		Topic: p.Topic,
		Key:   sarama.StringEncoder(event.AlertID),
		Value: sarama.ByteEncoder(data),
	}
	_, _, err = p.Producer.SendMessage(msg)
	if err != nil {
		p.Log.WithError(err).Error("failed to publish SOS event")
		return err
	}
	return nil
}
