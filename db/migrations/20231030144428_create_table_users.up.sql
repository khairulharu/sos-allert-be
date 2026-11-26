-- users: account rows. Session tokens live in Redis (see 20261114000000).
-- Postgres port of 20231030144428_create_table_users.up.sql (MySQL/InnoDB).
create table users
(
    id         varchar(100) not null,
    name       varchar(100) not null,
    password   varchar(100) not null,
    token      varchar(100) null,
    created_at bigint       not null,
    updated_at bigint       not null,
    primary key (id)
);
