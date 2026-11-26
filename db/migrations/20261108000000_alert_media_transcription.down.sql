ALTER TABLE alert_media
    DROP COLUMN IF EXISTS transcript,
    DROP COLUMN IF EXISTS transcription_status,
    DROP COLUMN IF EXISTS transcribed_at;
