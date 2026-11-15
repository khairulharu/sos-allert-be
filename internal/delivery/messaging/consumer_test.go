package messaging

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/sirupsen/logrus"
)

// stubSession is the minimum sarama.ConsumerGroupSession the retry loop
// touches: MarkMessage and Context.
type stubSession struct {
	ctx        context.Context
	marked     []*sarama.ConsumerMessage
	commits    int
	generation int32
	memberID   string
	mu         sync.Mutex
}

func newStubSession(ctx context.Context) *stubSession {
	return &stubSession{ctx: ctx, generation: 1, memberID: "member-1"}
}

func (s *stubSession) Claims() map[string][]int32 {
	return map[string][]int32{"sos.dispatched": {0}}
}

func (s *stubSession) MemberID() string { return s.memberID }

func (s *stubSession) GenerationID() int32 { return s.generation }

func (s *stubSession) MarkOffset(string, int32, int64, string) {}

func (s *stubSession) Commit() {
	s.mu.Lock()
	s.commits++
	s.mu.Unlock()
}

func (s *stubSession) ResetOffset(string, int32, int64, string) {}

func (s *stubSession) MarkMessage(message *sarama.ConsumerMessage, _ string) {
	s.mu.Lock()
	s.marked = append(s.marked, message)
	s.mu.Unlock()
}

func (s *stubSession) Context() context.Context { return s.ctx }

func (s *stubSession) markedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.marked)
}

// stubClaim replays the given messages and then closes the channel, which
// makes ConsumeClaim return like a finished partition claim.
type stubClaim struct {
	topic     string
	partition int32
	messages  chan *sarama.ConsumerMessage
}

func newStubClaim(messages ...*sarama.ConsumerMessage) *stubClaim {
	claim := &stubClaim{topic: "sos.dispatched", messages: make(chan *sarama.ConsumerMessage, len(messages))}
	for _, message := range messages {
		claim.messages <- message
	}
	close(claim.messages)
	return claim
}

func (c *stubClaim) Topic() string                            { return c.topic }
func (c *stubClaim) Partition() int32                         { return c.partition }
func (c *stubClaim) InitialOffset() int64                     { return 0 }
func (c *stubClaim) HighWaterMarkOffset() int64               { return 0 }
func (c *stubClaim) Messages() <-chan *sarama.ConsumerMessage { return c.messages }

// fakeHandler fails the first failures calls and succeeds afterwards.
type fakeHandler struct {
	failures int
	err      error
	calls    int
	values   [][]byte
	onCall   func(call int)
	mu       sync.Mutex
}

func (f *fakeHandler) handle(message *sarama.ConsumerMessage) error {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.values = append(f.values, message.Value)
	onCall := f.onCall
	f.mu.Unlock()

	if onCall != nil {
		onCall(call)
	}
	if call <= f.failures {
		return f.err
	}
	return nil
}

func (f *fakeHandler) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeProducer captures dead-letter records and can be made to fail.
type fakeProducer struct {
	messages []*sarama.ProducerMessage
	err      error
	mu       sync.Mutex
}

func (f *fakeProducer) SendMessage(message *sarama.ProducerMessage) (int32, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, message)
	if f.err != nil {
		return 0, 0, f.err
	}
	return 0, int64(len(f.messages) - 1), nil
}

func (f *fakeProducer) sent() []*sarama.ProducerMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*sarama.ProducerMessage(nil), f.messages...)
}

func testMessage(value string) *sarama.ConsumerMessage {
	return &sarama.ConsumerMessage{
		Topic:     "sos.dispatched",
		Partition: 2,
		Offset:    42,
		Key:       []byte("alert-1"),
		Value:     []byte(value),
		Headers:   []*sarama.RecordHeader{{Key: []byte("trace-id"), Value: []byte("abc")}},
	}
}

func newTestHandler(log *logrus.Logger, handler ConsumerHandler, options ConsumerOptions) *ConsumerGroupHandler {
	return &ConsumerGroupHandler{Handler: handler, Log: log, Options: options}
}

// A transient notifier failure must retry the same message, not skip it.
func TestConsumeClaimRetriesSameMessageThenMarks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := newStubSession(ctx)
	message := testMessage(`{"alert_id":"a1"}`)
	handler := &fakeHandler{failures: 2, err: errors.New("fcm 503")}
	group := newTestHandler(logrus.New(), handler.handle, ConsumerOptions{
		MaxRetries:   3,
		RetryBackoff: time.Millisecond,
	})

	if err := group.ConsumeClaim(session, newStubClaim(message)); err != nil {
		t.Fatalf("ConsumeClaim error = %v", err)
	}

	if got := handler.callCount(); got != 3 {
		t.Fatalf("handler calls = %d, want 3 (two failures then success)", got)
	}
	for i, value := range handler.values {
		if string(value) != string(message.Value) {
			t.Fatalf("call %d saw value %q, want the same message %q", i+1, value, message.Value)
		}
	}
	if got := session.markedCount(); got != 1 {
		t.Fatalf("marked messages = %d, want exactly 1", got)
	}
	if session.marked[0] != message {
		t.Fatal("a different message was marked than the one that was processed")
	}
}

// The retry limit must be honoured: no success, no more attempts.
func TestConsumeClaimStopsRetryingAtTheLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := newStubSession(ctx)
	handler := &fakeHandler{failures: 100, err: errors.New("twilio timeout")}
	producer := &fakeProducer{}
	group := newTestHandler(logrus.New(), handler.handle, ConsumerOptions{
		MaxRetries:   2,
		RetryBackoff: time.Millisecond,
		DeadLetter:   NewDeadLetter(producer, "sos.dispatched.dlq", nil),
	})

	if err := group.ConsumeClaim(session, newStubClaim(testMessage(`{"a":1}`))); err != nil {
		t.Fatalf("ConsumeClaim error = %v", err)
	}
	if got := handler.callCount(); got != 3 {
		t.Fatalf("handler calls = %d, want 3 (first attempt + 2 retries)", got)
	}
	if got := len(producer.sent()); got != 1 {
		t.Fatalf("dead-letter records = %d, want 1", got)
	}
}

// After the retries the original payload goes to the DLQ byte-for-byte,
// with the source coordinates and the last error in headers, and only
// then is the offset marked.
func TestConsumeClaimDeadLettersOriginalPayloadThenMarks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := newStubSession(ctx)
	message := testMessage(`{"alert_id":"a1","recipients":["user-b"]}`)
	lastErr := errors.New("twilio: 500 internal server error")
	handler := &fakeHandler{failures: 100, err: lastErr}
	producer := &fakeProducer{}

	var markBeforeDLQ bool
	group := newTestHandler(logrus.New(), handler.handle, ConsumerOptions{
		MaxRetries:   1,
		RetryBackoff: time.Millisecond,
		DeadLetter:   NewDeadLetter(producer, "sos.dispatched.dlq", logrus.New()),
	})
	handler.onCall = func(int) { markBeforeDLQ = session.markedCount() > 0 }

	if err := group.ConsumeClaim(session, newStubClaim(message)); err != nil {
		t.Fatalf("ConsumeClaim error = %v", err)
	}

	records := producer.sent()
	if len(records) != 1 {
		t.Fatalf("dead-letter records = %d, want 1", len(records))
	}
	record := records[0]
	if record.Topic != "sos.dispatched.dlq" {
		t.Fatalf("dead-letter topic = %q, want sos.dispatched.dlq", record.Topic)
	}
	value, err := record.Value.Encode()
	if err != nil {
		t.Fatalf("encode dead-letter value: %v", err)
	}
	if string(value) != string(message.Value) {
		t.Fatalf("dead-letter value = %q, want the original payload %q", value, message.Value)
	}
	key, err := record.Key.Encode()
	if err != nil {
		t.Fatalf("encode dead-letter key: %v", err)
	}
	if string(key) != "alert-1" {
		t.Fatalf("dead-letter key = %q, want the original key alert-1", key)
	}

	headers := map[string]string{}
	for _, header := range record.Headers {
		headers[string(header.Key)] = string(header.Value)
	}
	wantHeaders := map[string]string{
		"trace-id":             "abc", // original headers survive the move
		"dlq.source.topic":     "sos.dispatched",
		"dlq.source.partition": "2",
		"dlq.source.offset":    "42",
		"dlq.error":            lastErr.Error(),
	}
	for key, want := range wantHeaders {
		if got := headers[key]; got != want {
			t.Fatalf("dead-letter header %q = %q, want %q (headers %v)", key, got, want, headers)
		}
	}

	if got := session.markedCount(); got != 1 {
		t.Fatalf("marked messages = %d, want 1 after a successful dead-letter write", got)
	}
	if markBeforeDLQ {
		t.Fatal("message was marked before it reached the dead-letter topic")
	}
	if got := handler.callCount(); got != 2 {
		t.Fatalf("handler calls = %d, want 2 (first attempt + 1 retry)", got)
	}
}

// A dead-letter write failure must leave the offset uncommitted so the
// message can be redelivered, instead of silently dropping the alert.
func TestConsumeClaimLeavesMessageUnmarkedWhenDeadLetterFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := newStubSession(ctx)
	handler := &fakeHandler{failures: 100, err: errors.New("fcm 503")}
	dlqErr := errors.New("kafka: broker not available")
	producer := &fakeProducer{err: dlqErr}
	group := newTestHandler(logrus.New(), handler.handle, ConsumerOptions{
		MaxRetries:   1,
		RetryBackoff: time.Millisecond,
		DeadLetter:   NewDeadLetter(producer, "sos.dispatched.dlq", nil),
	})

	err := group.ConsumeClaim(session, newStubClaim(testMessage(`{"a":1}`)))
	if !errors.Is(err, dlqErr) {
		t.Fatalf("ConsumeClaim error = %v, want the dead-letter error", err)
	}
	if got := session.markedCount(); got != 0 {
		t.Fatalf("marked messages = %d, want 0 so the message is redelivered", got)
	}
}

// Without a DLQ producer the message is dropped after the retries (and
// marked) instead of panicking or stalling the partition forever.
func TestConsumeClaimDropsWithoutDeadLetterTopic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := newStubSession(ctx)
	handler := &fakeHandler{failures: 100, err: errors.New("fcm 503")}
	group := newTestHandler(logrus.New(), handler.handle, ConsumerOptions{
		MaxRetries:   1,
		RetryBackoff: time.Millisecond,
		DeadLetter:   NewDeadLetter((*fakeProducer)(nil), "", nil),
	})

	if err := group.ConsumeClaim(session, newStubClaim(testMessage(`{"a":1}`))); err != nil {
		t.Fatalf("ConsumeClaim error = %v", err)
	}
	if got := handler.callCount(); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
	if got := session.markedCount(); got != 1 {
		t.Fatalf("marked messages = %d, want 1 for a logged drop", got)
	}
}

// Shutdown during a retry wait must not mark the message: the next
// session replays it.
func TestConsumeClaimStopsPromptlyWhenSessionContextCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session := newStubSession(ctx)
	handler := &fakeHandler{failures: 100, err: errors.New("fcm 503")}
	group := newTestHandler(logrus.New(), handler.handle, ConsumerOptions{
		MaxRetries:   3,
		RetryBackoff: time.Hour, // would hang the test if it were not cancelled
	})
	handler.onCall = func(call int) {
		if call == 1 {
			cancel()
		}
	}

	done := make(chan error, 1)
	go func() { done <- group.ConsumeClaim(session, newStubClaim(testMessage(`{"a":1}`))) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ConsumeClaim error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ConsumeClaim did not return after the session context was cancelled")
	}

	if got := handler.callCount(); got != 1 {
		t.Fatalf("handler calls = %d, want 1 before the shutdown", got)
	}
	if got := session.markedCount(); got != 0 {
		t.Fatalf("marked messages = %d, want 0 so the message is redelivered", got)
	}
}

// The zero-value options are safe: the documented defaults apply.
func TestConsumerOptionsDefaults(t *testing.T) {
	options := ConsumerOptions{}
	if got := options.retries(); got != DefaultMaxRetries {
		t.Fatalf("retries = %d, want %d", got, DefaultMaxRetries)
	}
	if got := options.backoff(); got != DefaultRetryBackoff {
		t.Fatalf("backoff = %v, want %v", got, DefaultRetryBackoff)
	}
	if options.DeadLetter.enabled() {
		t.Fatal("a nil dead-letter publisher must not be enabled")
	}

	noRetry := ConsumerOptions{MaxRetries: -1, RetryBackoff: -1}
	if got := noRetry.retries(); got != 0 {
		t.Fatalf("retries = %d, want 0 when disabled", got)
	}
	if got := noRetry.backoff(); got != 0 {
		t.Fatalf("backoff = %v, want 0 when disabled", got)
	}
}

// Kafka is optional (#42): a nil consumer group (unreachable broker at
// startup) must make the topic be skipped instead of panicking on a nil
// interface call or blocking the process.
func TestConsumeTopicSkipsNilConsumerGroup(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)

	done := make(chan struct{})
	go func() {
		defer close(done)
		ConsumeTopic(context.Background(), nil, "users", log, func(*sarama.ConsumerMessage) error { return nil },
			ConsumerOptions{})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ConsumeTopic with a nil consumer group did not return")
	}
}
