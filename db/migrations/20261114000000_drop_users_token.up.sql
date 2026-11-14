-- Session tokens moved to Redis (auth:token:<token> -> user id and
-- auth:user:<user_id> -> token) with a TTL, so plaintext sessions are no
-- longer stored in MySQL. Deploying this change invalidates every existing
-- token: users must log in again.
ALTER TABLE users DROP COLUMN token;
