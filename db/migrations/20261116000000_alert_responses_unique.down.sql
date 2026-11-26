DROP INDEX IF EXISTS uq_alert_responses_alert_responder;

CREATE INDEX idx_responses_alert_responder ON alert_responses (alert_id, responder_id);
