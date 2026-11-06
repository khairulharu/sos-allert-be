alter table alert_media
    add column transcript text null,
    add column transcription_status varchar(20) not null default 'disabled',
    add column transcribed_at bigint null;
