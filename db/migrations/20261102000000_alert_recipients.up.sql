-- Users notified for an incident, so responders keep an inbox after the push
-- is dismissed (GET /api/sos/inbox).
CREATE TABLE alert_recipients
(
    alert_id    varchar(100) not null,
    user_id     varchar(100) not null,
    notified_at bigint not null,
    primary key (alert_id, user_id),
    constraint fk_alert_recipients_alert_id foreign key (alert_id) references alerts (id) on delete cascade
);

CREATE INDEX idx_alert_recipients_user_time ON alert_recipients (user_id, notified_at);
