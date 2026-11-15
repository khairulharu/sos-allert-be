-- One answer per responder per alert (#42). AlertResponse was always
-- inserted and alert_responses only had a non-unique (alert_id, responder_id)
-- index, so a double tap or a retried POST stored duplicate responders and
-- inflated responders_en_route. The unique index makes the insert idempotent:
-- a repeat answer is a no-op the repository reports as 409.
--
-- Collapse existing duplicates first (the earliest answer wins), otherwise
-- the unique index cannot be created on a populated table.
DELETE dup
FROM alert_responses dup
         JOIN alert_responses keep
              ON keep.alert_id = dup.alert_id
                  AND keep.responder_id = dup.responder_id
                  AND (keep.responded_at < dup.responded_at
                      OR (keep.responded_at = dup.responded_at AND keep.id < dup.id));

-- The unique index covers the same (alert_id, responder_id) prefix, so the
-- redundant non-unique index goes away.
ALTER TABLE alert_responses
    DROP INDEX idx_responses_alert_responder,
    ADD UNIQUE INDEX uq_alert_responses_alert_responder (alert_id, responder_id);
