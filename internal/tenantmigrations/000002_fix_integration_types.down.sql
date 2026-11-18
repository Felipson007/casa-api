-- Reverte D-013: volta ao CHECK original de 000001.
-- ATENCAO: falha se existirem linhas com os tipos reais (GEMINI/GROQ/etc).

ALTER TABLE external_integrations DROP CONSTRAINT IF EXISTS external_integrations_type_check;
ALTER TABLE external_integrations
    ADD CONSTRAINT external_integrations_type_check
    CHECK (type IN ('IMMICH', 'TELEGRAM', 'OPENAI', 'OTHER'));

ALTER TABLE system_integrations DROP CONSTRAINT IF EXISTS system_integrations_type_check;
ALTER TABLE system_integrations
    ADD CONSTRAINT system_integrations_type_check
    CHECK (type IN ('IMMICH', 'TELEGRAM', 'OPENAI', 'OTHER'));
