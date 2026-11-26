DROP INDEX IF EXISTS idx_alerts_dispatch_due;

ALTER TABLE alerts
    DROP COLUMN IF EXISTS dispatch_due_at,
    DROP COLUMN IF EXISTS dispatched_at;
