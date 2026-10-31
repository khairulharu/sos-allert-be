DROP TABLE alert_media;
DROP TABLE alert_responses;
DROP TABLE emergency_contacts;
DROP TABLE alert_breadcrumbs;

ALTER TABLE users
    DROP COLUMN safe_pin_hash,
    DROP COLUMN fcm_token,
    DROP COLUMN apns_token;

ALTER TABLE alerts
    DROP INDEX idx_alerts_sender_status,
    DROP INDEX idx_alerts_status_triggered_at,
    DROP COLUMN status,
    DROP COLUMN emergency_type,
    DROP COLUMN accuracy_meters,
    DROP COLUMN battery_percentage,
    DROP COLUMN cancellation_reason,
    DROP COLUMN resolved_at,
    DROP COLUMN cancelled_at;
