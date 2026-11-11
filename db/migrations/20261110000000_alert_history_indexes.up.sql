-- Alert history: sender timeline paging and per-responder "helped" counts.
-- (status, triggered_at) and (sender_id, status) already exist in
-- 20261031000000_alerts_v2, so only these two are new.
ALTER TABLE alerts
    ADD INDEX idx_alerts_sender_triggered_at (sender_id, triggered_at);

ALTER TABLE alert_responses
    ADD INDEX idx_alert_responses_responder (responder_id, alert_id);
