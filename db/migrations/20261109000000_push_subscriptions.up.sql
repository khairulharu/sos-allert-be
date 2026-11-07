-- Browser Web Push subscriptions (VAPID). One row per browser install;
-- the unique endpoint makes re-subscribing from the same browser an
-- upsert that re-points the subscription at the current user.
CREATE TABLE push_subscriptions (
    id         VARCHAR(100) NOT NULL,
    user_id    VARCHAR(100) NOT NULL,
    endpoint   VARCHAR(512) NOT NULL,
    p256dh     VARCHAR(255) NOT NULL,
    auth       VARCHAR(255) NOT NULL,
    user_agent VARCHAR(255) NULL,
    created_at BIGINT       NOT NULL,
    updated_at BIGINT       NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_push_subscriptions_endpoint (endpoint),
    INDEX idx_push_subscriptions_user (user_id),
    CONSTRAINT fk_push_subscriptions_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE = InnoDB;
