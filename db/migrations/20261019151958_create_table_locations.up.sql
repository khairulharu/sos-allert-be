create table locations
(
    id         varchar(100) not null,
    user_id    varchar(100) not null,
    longitude  double       not null,
    latitude   double       not null,
    created_at bigint       not null,
    updated_at bigint       not null,
    primary key (id),
    foreign key fk_locations_user_id (user_id) references users (id)
) engine = innodb;
