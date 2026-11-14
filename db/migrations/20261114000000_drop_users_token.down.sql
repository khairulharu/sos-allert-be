-- Restores the column for rollback; the previous tokens cannot be restored,
-- so users log in again after a downgrade too.
ALTER TABLE users ADD COLUMN token VARCHAR(100) NULL;
