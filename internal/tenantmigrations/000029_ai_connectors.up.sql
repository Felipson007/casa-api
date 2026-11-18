-- Per-tenant AI connectors (BYOK): the tenant chooses their provider (OmniRoute, OpenAI, Claude,
-- Groq, OpenRouter, ...) and stores their own API key. The key is encrypted with the tenant's DEK
-- (internal/platform/keyring), never stored in clear.
CREATE TABLE ai_connectors (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider          varchar(30) NOT NULL CHECK (provider IN
                          ('OMNIROUTE', 'OPENAI', 'ANTHROPIC', 'GROQ', 'OPENROUTER', 'DEEPSEEK', 'GOOGLE', 'CUSTOM')),
    label             varchar(100) NOT NULL DEFAULT '',
    base_url          text NOT NULL DEFAULT '',
    api_key_encrypted bytea,
    auth_style        varchar(20) NOT NULL DEFAULT 'BEARER' CHECK (auth_style IN ('BEARER', 'X_API_KEY')),
    chat_model        varchar(150) NOT NULL DEFAULT '',
    extraction_model  varchar(150) NOT NULL DEFAULT '',
    priority          smallint NOT NULL DEFAULT 100,
    enabled           boolean NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_ai_connectors_priority ON ai_connectors (enabled, priority);
