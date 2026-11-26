-- alerts v2: lifecycle status, emergency type, telemetry, cancellation/resolution.
-- MySQL FLOAT maps to `real`; DOUBLE maps to `double precision`.
ALTER TABLE alerts
    ADD COLUMN status VARCHAR(20) NOT NULL DEFAULT 'TRIGGERED',
    ADD COLUMN emergency_type VARCHAR(30) NOT NULL DEFAULT 'DANGER',
    ADD COLUMN accuracy_meters REAL NULL,
    ADD COLUMN battery_percentage INT NULL,
    ADD COLUMN cancellation_reason VARCHAR(100) NULL,
    ADD COLUMN resolved_at BIGINT NULL,
    ADD COLUMN cancelled_at BIGINT NULL;

-- MySQL added these through ADD INDEX; Postgres needs separate statements.
CREATE INDEX idx_alerts_status_triggered_at ON alerts (status, triggered_at);
CREATE INDEX idx_alerts_sender_status ON alerts (sender_id, status);

-- Breadcrumb trajectory trail.
CREATE TABLE alert_breadcrumbs
(
    id                 bigserial primary key,
    alert_id           varchar(100) not null,
    latitude           double precision not null,
    longitude          double precision not null,
    speed_mps          real null,
    heading_deg        real null,
    altitude_meters    real null,
    accuracy_meters    real null,
    battery_percentage int null,
    recorded_at        bigint not null,
    constraint fk_breadcrumbs_alert_id foreign key (alert_id) references alerts (id) on delete cascade
);

CREATE INDEX idx_breadcrumbs_alert_time ON alert_breadcrumbs (alert_id, recorded_at);

-- Tier-1 emergency contacts for SMS/voice fallback.
CREATE TABLE emergency_contacts
(
    id            varchar(100) not null,
    user_id       varchar(100) not null,
    contact_name  varchar(100) not null,
    phone_number  varchar(30) not null,
    relationship  varchar(50) not null,
    is_verified   boolean not null default false,
    created_at    bigint not null,
    primary key (id),
    constraint fk_emergency_contacts_user_id foreign key (user_id) references users (id) on delete cascade
);

CREATE INDEX idx_emergency_contacts_user_id ON emergency_contacts (user_id);

-- Responder answers with ETA and distance at response time.
CREATE TABLE alert_responses
(
    id                  varchar(100) not null,
    alert_id            varchar(100) not null,
    responder_id        varchar(100) not null,
    responder_latitude  double precision not null,
    responder_longitude double precision not null,
    distance_meters     double precision not null,
    eta_seconds         int not null,
    responded_at        bigint not null,
    primary key (id),
    constraint fk_responses_alert_id foreign key (alert_id) references alerts (id) on delete cascade
);

CREATE INDEX idx_responses_alert_id ON alert_responses (alert_id);
CREATE INDEX idx_responses_alert_responder ON alert_responses (alert_id, responder_id);

-- Uploaded incident media metadata (bytes live in the storage backend).
CREATE TABLE alert_media
(
    id            varchar(100) not null,
    alert_id      varchar(100) not null,
    filename      varchar(255) not null,
    content_type  varchar(100) not null,
    size_bytes    bigint not null,
    storage_path  varchar(500) not null,
    uploaded_by   varchar(100) not null,
    uploaded_at   bigint not null,
    primary key (id),
    constraint fk_media_alert_id foreign key (alert_id) references alerts (id) on delete cascade
);

CREATE INDEX idx_media_alert_id ON alert_media (alert_id);

-- Safe PIN (bcrypt hash, never the raw PIN) and push device tokens.
ALTER TABLE users
    ADD COLUMN safe_pin_hash VARCHAR(100) NULL,
    ADD COLUMN fcm_token VARCHAR(255) NULL,
    ADD COLUMN apns_token VARCHAR(255) NULL;
