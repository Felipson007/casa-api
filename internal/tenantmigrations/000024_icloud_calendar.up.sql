-- ============================================================================================
-- iCloud Calendar (CalDAV) — one more provider in Configurações -> Conexões, stored like
-- Telegram/Immich/Pluggy: credentials AES-GCM encrypted in external_integrations.
--
-- external_synced_at records when a row was last reconciled with the external calendar, so the
-- sync can tell "edited locally since the last sync" (updated_at > external_synced_at) from "just
-- written by the sync itself" (updated_at == external_synced_at) without depending on the remote
-- clock agreeing with ours. external_href is the CalDAV resource path used to PUT/DELETE.
-- ============================================================================================
ALTER TABLE external_integrations DROP CONSTRAINT IF EXISTS external_integrations_type_check;
ALTER TABLE external_integrations
    ADD CONSTRAINT external_integrations_type_check
    CHECK (type IN ('GOOGLE_DRIVE','GEMINI','DEEPSEEK','GROQ','OPENROUTER','TELEGRAM','IMMICH','OPENAI','OTHER','PLUGGY','ICLOUD_CALENDAR'));

ALTER TABLE calendar_events ADD COLUMN external_synced_at timestamptz;
ALTER TABLE calendar_events ADD COLUMN external_href varchar(512);
