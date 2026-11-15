package usecase

import "github.com/sirupsen/logrus"

// publishAfterCommit propagates a committed change to Kafka. The row is
// already durable when this runs, so a publish failure must not fail the
// request (#42): returning it made register answer 500 for a user that was
// created (a retry then 409), reported a failed update for saved data, and
// reported a failed logout after the session had already been deleted.
// The event is lost with Kafka down (the worker would not see it), so log it
// and keep the successful response.
//
// The nil-producer check stays at the call site: "Kafka disabled" is a
// configured state, not a publish failure.
func publishAfterCommit(log *logrus.Logger, event string, send func() error) {
	if err := send(); err != nil {
		log.WithError(err).Warnf("could not publish %s event after commit; the change is already saved", event)
	}
}
