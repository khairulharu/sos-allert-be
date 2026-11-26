-- Persist the optional AI triage result on the incident so cold reads
-- (detail, active list, inbox) show the same severity/summary as push.
-- MySQL used `AFTER emergency_type`; Postgres appends the columns instead.
-- severity NULL means "never classified" and is distinct from a score of 0.
ALTER TABLE alerts
    ADD COLUMN severity DOUBLE PRECISION NULL,
    ADD COLUMN summary VARCHAR(512) NULL;
