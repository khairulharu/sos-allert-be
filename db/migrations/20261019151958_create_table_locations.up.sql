create table contacts
(
    id         varchar(100) not null,
    user_id    varchar(100) not null,
    longitude varchar(100) not null,
    latitude  varchar(100) not null,
    created_at bigint       not null,
    updated_at bigint       not null,
    primary key (id),
    foreign key fk_contacts_user_id (user_id) references users (id)
) engine = innodb;
