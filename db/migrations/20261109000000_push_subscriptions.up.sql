-- Browser Web Push subscriptions (VAPID). One row per browser install; the
-- unique endpoint makes re-subscribing from the same browser an upsert that
-- re-points the subscription at the current user.
CREATE TABLE push_subscriptions
(
    id         varchar(100) not null,
    user_id    varchar(100) not null,
    endpoint   varchar(512) not null,
    p256dh     varchar(255) not null,
    auth       varchar(255) not null,
    user_agent varchar(255) null,
    created_at bigint not null,
    updated_at bigint not null,
    primary key (id),
    constraint uq_push_subscriptions_endpoint unique (endpoint),
    constraint fk_push_subscriptions_user foreign key (user_id) references users (id) on delete cascade
);

CREATE INDEX idx_push_subscriptions_user ON push_subscriptions (user_id);
