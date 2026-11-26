-- Contacts belong to one user. MySQL created an index for the FK implicitly;
-- Postgres does not, so idx_contacts_user_id is added explicitly to keep
-- `select ... where user_id = $1` off a seq scan.
create table contacts
(
    id         varchar(100) not null,
    first_name varchar(100) not null,
    last_name  varchar(100) null,
    email      varchar(100) null,
    phone      varchar(100) null,
    user_id    varchar(100) not null,
    created_at bigint       not null,
    updated_at bigint       not null,
    primary key (id),
    constraint fk_contacts_user_id foreign key (user_id) references users (id)
);

create index idx_contacts_user_id on contacts (user_id);
