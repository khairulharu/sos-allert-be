-- Incident rows. Timestamps are millisecond bigint, same convention as the
-- Go service, so an import from MySQL needs no timezone step.
create table alerts
(
    id           varchar(100) not null,
    sender_id    varchar(100) not null,
    longitude    double precision not null,
    latitude     double precision not null,
    triggered_at bigint       not null,
    primary key (id),
    constraint fk_alerts_sender_id foreign key (sender_id) references users (id)
);
