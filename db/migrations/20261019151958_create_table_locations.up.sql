-- Last known position per user, written on every heartbeat. Live radius search
-- reads Redis GEO instead; this table is history plus the fallback for
-- senders that omit coordinates.
create table locations
(
    id         varchar(100) not null,
    user_id    varchar(100) not null,
    longitude  double precision not null,
    latitude   double precision not null,
    created_at bigint       not null,
    updated_at bigint       not null,
    primary key (id),
    constraint fk_locations_user_id foreign key (user_id) references users (id)
);

create index idx_locations_user_id on locations (user_id);
