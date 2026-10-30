create table alerts
(
    id           varchar(100) not null,
    sender_id    varchar(100) not null,
    longitude    double       not null,
    latitude     double       not null,
    triggered_at bigint       not null,
    primary key (id),
    foreign key fk_alerts_sender_id (sender_id) references users (id)
) engine = innodb;
