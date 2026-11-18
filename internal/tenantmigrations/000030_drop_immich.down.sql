-- Reverses 000030_drop_immich: restores the columns and the IMMICH enum value in both type CHECKs.
ALTER TABLE people ADD COLUMN IF NOT EXISTS immich_person_id varchar(100);

ALTER TABLE external_integrations ADD COLUMN IF NOT EXISTS immich_base_url varchar(255);

ALTER TABLE external_integrations DROP CONSTRAINT IF EXISTS external_integrations_type_check;
ALTER TABLE external_integrations
    ADD CONSTRAINT external_integrations_type_check
    CHECK (type IN ('GOOGLE_DRIVE','GEMINI','DEEPSEEK','GROQ','OPENROUTER','TELEGRAM','IMMICH','OPENAI','OTHER','PLUGGY','ICLOUD_CALENDAR','GOOGLE_CALENDAR'));

ALTER TABLE system_integrations DROP CONSTRAINT IF EXISTS system_integrations_type_check;
ALTER TABLE system_integrations
    ADD CONSTRAINT system_integrations_type_check
    CHECK (type IN ('GOOGLE_DRIVE','GEMINI','DEEPSEEK','GROQ','OPENROUTER','TELEGRAM','IMMICH','OPENAI','OTHER','PLUGGY','ICLOUD_CALENDAR','GOOGLE_CALENDAR'));
