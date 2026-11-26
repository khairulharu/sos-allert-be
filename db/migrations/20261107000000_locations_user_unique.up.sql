-- One location row per user, so the heartbeat is an upsert instead of a new row
-- per update. MySQL `add unique key` -> create unique index.
CREATE UNIQUE INDEX uq_locations_user_id ON locations (user_id);
