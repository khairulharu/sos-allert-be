-- AI triage columns on alerts (competition MVP). The original report
-- text is stored so responders always see the reporter's own words,
-- not only the AI summary. Run after 20261031000000_alerts_v2.
ALTER TABLE alerts
    ADD COLUMN ai_category VARCHAR(30) NOT NULL DEFAULT '',
    ADD COLUMN ai_severity INT NOT NULL DEFAULT 0,
    ADD COLUMN ai_confidence DOUBLE NOT NULL DEFAULT 0,
    ADD COLUMN ai_language VARCHAR(20) NOT NULL DEFAULT '',
    ADD COLUMN ai_summary_id VARCHAR(500) NOT NULL DEFAULT '',
    ADD COLUMN ai_summary_en VARCHAR(500) NOT NULL DEFAULT '',
    ADD COLUMN ai_original_text VARCHAR(500) NOT NULL DEFAULT '',
    ADD COLUMN ai_source VARCHAR(20) NOT NULL DEFAULT '',
    ADD COLUMN ai_needs_clarification BOOLEAN NOT NULL DEFAULT FALSE,
    ADD INDEX idx_alerts_ai_category (ai_category);
