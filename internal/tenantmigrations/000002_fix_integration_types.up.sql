-- ============================================================================================
-- D-013: o CHECK de type em external_integrations/system_integrations foi criado com
-- ('IMMICH','TELEGRAM','OPENAI','OTHER'), mas o enum real do Java
-- (br.com.atlas.integracoes.domain.TipoIntegracao) tem 7 valores:
-- GOOGLE_DRIVE, GEMINI, DEEPSEEK, GROQ, OPENROUTER, TELEGRAM, IMMICH.
-- 'OPENAI' nunca existiu no Java (provavelmente inferido de "API compativel com OpenAI").
-- Alargamento puramente ADITIVO: mantemos OPENAI e OTHER para nao invalidar linha existente
-- nem quebrar seed/dump antigo; a validacao estreita (7 valores reais) fica no Go layer,
-- mesmo padrao de documents.Origin / health.RecordType / notifications.Type.
-- ============================================================================================

ALTER TABLE external_integrations DROP CONSTRAINT IF EXISTS external_integrations_type_check;
ALTER TABLE external_integrations
    ADD CONSTRAINT external_integrations_type_check
    CHECK (type IN ('GOOGLE_DRIVE','GEMINI','DEEPSEEK','GROQ','OPENROUTER','TELEGRAM','IMMICH','OPENAI','OTHER'));

ALTER TABLE system_integrations DROP CONSTRAINT IF EXISTS system_integrations_type_check;
ALTER TABLE system_integrations
    ADD CONSTRAINT system_integrations_type_check
    CHECK (type IN ('GOOGLE_DRIVE','GEMINI','DEEPSEEK','GROQ','OPENROUTER','TELEGRAM','IMMICH','OPENAI','OTHER'));
