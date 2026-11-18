ALTER TABLE calendar_events DROP COLUMN IF EXISTS external_href;
ALTER TABLE calendar_events DROP COLUMN IF EXISTS external_synced_at;

DELETE FROM external_integrations WHERE type = 'ICLOUD_CALENDAR';

ALTER TABLE external_integrations DROP CONSTRAINT IF EXISTS external_integrations_type_check;
ALTER TABLE external_integrations
    ADD CONSTRAINT external_integrations_type_check
    CHECK (type IN ('GOOGLE_DRIVE','GEMINI','DEEPSEEK','GROQ','OPENROUTER','TELEGRAM','IMMICH','OPENAI','OTHER','PLUGGY'));
