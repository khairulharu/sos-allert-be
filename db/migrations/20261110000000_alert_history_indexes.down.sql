ALTER TABLE alert_responses
    DROP INDEX idx_alert_responses_responder;

ALTER TABLE alerts
    DROP INDEX idx_alerts_sender_triggered_at;
