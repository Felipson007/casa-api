-- ============================================================================================
-- Google Calendar (two-way) + the global calendar sync target.
--
-- GOOGLE_CALENDAR joins ICLOUD_CALENDAR as a calendar provider in external_integrations (OAuth
-- refresh token + selected calendar, AES-GCM encrypted like every other credential).
--
-- calendar_sync_settings is a singleton: the household picks ONE destination for the whole
-- calendar (NONE = local only, ICLOUD or GOOGLE). The worker only runs the selected provider's
-- sync, so the two providers never fight over the same rows.
-- ============================================================================================
ALTER TABLE external_integrations DROP CONSTRAINT IF EXISTS external_integrations_type_check;
ALTER TABLE external_integrations
    ADD CONSTRAINT external_integrations_type_check
    CHECK (type IN ('GOOGLE_DRIVE','GEMINI','DEEPSEEK','GROQ','OPENROUTER','TELEGRAM','IMMICH','OPENAI','OTHER','PLUGGY','ICLOUD_CALENDAR','GOOGLE_CALENDAR'));

CREATE TABLE calendar_sync_settings (
    id         boolean PRIMARY KEY DEFAULT true,
    target     varchar(10) NOT NULL DEFAULT 'NONE',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_calendar_sync_settings_singleton CHECK (id),
    CONSTRAINT ck_calendar_sync_settings_target CHECK (target IN ('NONE', 'ICLOUD', 'GOOGLE'))
);

INSERT INTO calendar_sync_settings (id) VALUES (true) ON CONFLICT (id) DO NOTHING;
