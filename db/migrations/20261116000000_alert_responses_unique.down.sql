ALTER TABLE alert_responses
    DROP INDEX uq_alert_responses_alert_responder,
    ADD INDEX idx_responses_alert_responder (alert_id, responder_id);
