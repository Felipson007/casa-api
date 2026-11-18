-- Removes the Immich integration entirely (the photo proxy was deleted): the per-person link column
-- and the per-integration base URL, plus the IMMICH value from both type CHECKs.
ALTER TABLE people DROP COLUMN IF EXISTS immich_person_id;

ALTER TABLE external_integrations DROP COLUMN IF EXISTS immich_base_url;

ALTER TABLE external_integrations DROP CONSTRAINT IF EXISTS external_integrations_type_check;
ALTER TABLE external_integrations
    ADD CONSTRAINT external_integrations_type_check
    CHECK (type IN ('GOOGLE_DRIVE','GEMINI','DEEPSEEK','GROQ','OPENROUTER','TELEGRAM','OPENAI','OTHER','PLUGGY','ICLOUD_CALENDAR','GOOGLE_CALENDAR'));

ALTER TABLE system_integrations DROP CONSTRAINT IF EXISTS system_integrations_type_check;
ALTER TABLE system_integrations
    ADD CONSTRAINT system_integrations_type_check
    CHECK (type IN ('GOOGLE_DRIVE','GEMINI','DEEPSEEK','GROQ','OPENROUTER','TELEGRAM','OPENAI','OTHER','PLUGGY','ICLOUD_CALENDAR','GOOGLE_CALENDAR'));
