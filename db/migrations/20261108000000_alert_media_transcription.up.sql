-- Audio STT results on the media row. `transcription_status` values:
-- disabled | pending | done | empty | failed.
ALTER TABLE alert_media
    ADD COLUMN transcript TEXT NULL,
    ADD COLUMN transcription_status VARCHAR(20) NOT NULL DEFAULT 'disabled',
    ADD COLUMN transcribed_at BIGINT NULL;
