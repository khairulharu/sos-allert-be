drop table if exists alert_media;
drop table if exists alert_responses;
drop table if exists emergency_contacts;
drop table if exists alert_breadcrumbs;

ALTER TABLE users
    DROP COLUMN IF EXISTS safe_pin_hash,
    DROP COLUMN IF EXISTS fcm_token,
    DROP COLUMN IF EXISTS apns_token;

DROP INDEX IF EXISTS idx_alerts_status_triggered_at;
DROP INDEX IF EXISTS idx_alerts_sender_status;

ALTER TABLE alerts
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS emergency_type,
    DROP COLUMN IF EXISTS accuracy_meters,
    DROP COLUMN IF EXISTS battery_percentage,
    DROP COLUMN IF EXISTS cancellation_reason,
    DROP COLUMN IF EXISTS resolved_at,
    DROP COLUMN IF EXISTS cancelled_at;
