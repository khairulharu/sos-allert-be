-- Alert history: sender timeline paging and per-responder "helped" counts.
-- (status, triggered_at) and (sender_id, status) already exist from
-- 20261031000000_alerts_v2, so only these two are new.
CREATE INDEX idx_alerts_sender_triggered_at ON alerts (sender_id, triggered_at);
CREATE INDEX idx_alert_responses_responder ON alert_responses (responder_id, alert_id);
