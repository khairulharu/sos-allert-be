-- alerts v2: lifecycle status, emergency type, telemetry, cancellation/resolution.
ALTER TABLE alerts
    ADD COLUMN status VARCHAR(20) NOT NULL DEFAULT 'TRIGGERED',
    ADD COLUMN emergency_type VARCHAR(30) NOT NULL DEFAULT 'DANGER',
    ADD COLUMN accuracy_meters FLOAT NULL,
    ADD COLUMN battery_percentage INT NULL,
    ADD COLUMN cancellation_reason VARCHAR(100) NULL,
    ADD COLUMN resolved_at BIGINT NULL,
    ADD COLUMN cancelled_at BIGINT NULL,
    ADD INDEX idx_alerts_status_triggered_at (status, triggered_at),
    ADD INDEX idx_alerts_sender_status (sender_id, status);

-- Breadcrumb trajectory trail (README section 5).
CREATE TABLE alert_breadcrumbs (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    alert_id VARCHAR(100) NOT NULL,
    latitude DOUBLE NOT NULL,
    longitude DOUBLE NOT NULL,
    speed_mps FLOAT NULL,
    heading_deg FLOAT NULL,
    altitude_meters FLOAT NULL,
    accuracy_meters FLOAT NULL,
    battery_percentage INT NULL,
    recorded_at BIGINT NOT NULL,
    FOREIGN KEY fk_breadcrumbs_alert_id (alert_id) REFERENCES alerts (id) ON DELETE CASCADE,
    INDEX idx_breadcrumbs_alert_time (alert_id, recorded_at)
) ENGINE = InnoDB;

-- Tier-1 emergency contacts for SMS/voice fallback (README section 5).
CREATE TABLE emergency_contacts (
    id VARCHAR(100) NOT NULL,
    user_id VARCHAR(100) NOT NULL,
    contact_name VARCHAR(100) NOT NULL,
    phone_number VARCHAR(30) NOT NULL,
    relationship VARCHAR(50) NOT NULL,
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (id),
    FOREIGN KEY fk_emergency_contacts_user_id (user_id) REFERENCES users (id) ON DELETE CASCADE,
    INDEX idx_emergency_contacts_user_id (user_id)
) ENGINE = InnoDB;

-- Responder answers with ETA and distance at response time.
CREATE TABLE alert_responses (
    id VARCHAR(100) NOT NULL,
    alert_id VARCHAR(100) NOT NULL,
    responder_id VARCHAR(100) NOT NULL,
    responder_latitude DOUBLE NOT NULL,
    responder_longitude DOUBLE NOT NULL,
    distance_meters DOUBLE NOT NULL,
    eta_seconds INT NOT NULL,
    responded_at BIGINT NOT NULL,
    PRIMARY KEY (id),
    FOREIGN KEY fk_responses_alert_id (alert_id) REFERENCES alerts (id) ON DELETE CASCADE,
    INDEX idx_responses_alert_id (alert_id),
    INDEX idx_responses_alert_responder (alert_id, responder_id)
) ENGINE = InnoDB;

-- Uploaded incident media metadata (bytes live in the storage backend).
CREATE TABLE alert_media (
    id VARCHAR(100) NOT NULL,
    alert_id VARCHAR(100) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    size_bytes BIGINT NOT NULL,
    storage_path VARCHAR(500) NOT NULL,
    uploaded_by VARCHAR(100) NOT NULL,
    uploaded_at BIGINT NOT NULL,
    PRIMARY KEY (id),
    FOREIGN KEY fk_media_alert_id (alert_id) REFERENCES alerts (id) ON DELETE CASCADE,
    INDEX idx_media_alert_id (alert_id)
) ENGINE = InnoDB;

-- Safe PIN (bcrypt hash, never the raw PIN) and push device tokens.
ALTER TABLE users
    ADD COLUMN safe_pin_hash VARCHAR(100) NULL,
    ADD COLUMN fcm_token VARCHAR(255) NULL,
    ADD COLUMN apns_token VARCHAR(255) NULL;
