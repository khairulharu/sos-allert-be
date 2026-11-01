-- Persist the optional AI triage result on the incident so cold reads
-- (detail, active list, inbox) show the same severity/summary as push.
ALTER TABLE alerts
    ADD COLUMN severity DOUBLE NULL AFTER emergency_type,
    ADD COLUMN summary VARCHAR(512) NULL AFTER severity;
