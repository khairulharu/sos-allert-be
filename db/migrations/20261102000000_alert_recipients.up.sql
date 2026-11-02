-- Users notified for an incident, so responders keep an inbox after the
-- push is dismissed (GET /api/sos/inbox).
CREATE TABLE alert_recipients (
    alert_id    VARCHAR(100) NOT NULL,
    user_id     VARCHAR(100) NOT NULL,
    notified_at BIGINT       NOT NULL,
    PRIMARY KEY (alert_id, user_id),
    FOREIGN KEY fk_alert_recipients_alert_id (alert_id) REFERENCES alerts (id) ON DELETE CASCADE,
    INDEX idx_alert_recipients_user_time (user_id, notified_at)
) ENGINE = InnoDB;
