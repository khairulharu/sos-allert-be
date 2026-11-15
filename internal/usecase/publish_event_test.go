package usecase

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"golang-clean-architecture/internal/gateway/messaging"
	"golang-clean-architecture/internal/model"

	"github.com/IBM/sarama"
	"github.com/sirupsen/logrus"
)

// erroringProducer fails every write, standing in for a Kafka outage. The
// embedded interface supplies the methods the publish path never calls.
type erroringProducer struct {
	sarama.SyncProducer
	calls int
}

func (p *erroringProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
	p.calls++
	return 0, 0, errors.New("kafka unavailable")
}

// A publish failure after the commit is logged and swallowed (#42): the data
// is already saved, so the caller must keep its successful response instead
// of turning a Kafka outage into a 500.
func TestPublishAfterCommitSwallowsProducerError(t *testing.T) {
	var logs bytes.Buffer
	log := logrus.New()
	log.SetOutput(&logs)
	log.SetLevel(logrus.WarnLevel)

	producer := &erroringProducer{}
	userProducer := messaging.NewUserProducer(producer, log)
	event := &model.UserEvent{ID: "user-1"}

	publishAfterCommit(log, "user created", func() error { return userProducer.Send(event) })

	if producer.calls != 1 {
		t.Fatalf("producer calls = %d, want 1 (the event must still be attempted)", producer.calls)
	}
	if !strings.Contains(logs.String(), "user created") {
		t.Fatalf("log output = %q, want the failed publish to be reported", logs.String())
	}
}

// A successful publish is still published exactly once.
func TestPublishAfterCommitSendsOnce(t *testing.T) {
	calls := 0
	publishAfterCommit(noopLogger(), "contact updated", func() error {
		calls++
		return nil
	})
	if calls != 1 {
		t.Fatalf("send calls = %d, want 1", calls)
	}
}
