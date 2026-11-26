-- Durable grace-period outbox: the post-grace fan-out used to be a timer in the
-- web process, so a restart inside the grace window left the alert TRIGGERED
-- and reached nobody. The due time is persisted with the alert and a polling
-- loop in the web process dispatches it (at-least-once: a crash after sending
-- but before marking re-sends; losing an SOS is worse than a duplicate).
--
-- dispatch_due_at is NULL when the alert was dispatched inline
-- (sos.grace_period_seconds = 0), so those rows never enter the scan.
ALTER TABLE alerts
    ADD COLUMN dispatch_due_at BIGINT NULL,
    ADD COLUMN dispatched_at BIGINT NULL;

-- Due-scan index: status = 'TRIGGERED' AND dispatch_due_at <= now
-- AND dispatched_at IS NULL, ordered by dispatch_due_at.
CREATE INDEX idx_alerts_dispatch_due ON alerts (status, dispatch_due_at, dispatched_at);
