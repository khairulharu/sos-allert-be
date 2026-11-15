ALTER TABLE alerts
    DROP INDEX idx_alerts_dispatch_due;

ALTER TABLE alerts
    DROP COLUMN dispatched_at,
    DROP COLUMN dispatch_due_at;
