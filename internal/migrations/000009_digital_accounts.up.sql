-- digital_accounts — inventory of external accounts/services (GitHub, banks, streaming, ...),
-- metadata only. See docs/gap-analysis-vida-digital-20260822.md GAP 1.
--
-- Deliberate design rule, not an oversight: THIS TABLE NEVER STORES A SECRET. No password/token/
-- api_key column exists here and none should ever be added — Atlas records where an account lives
-- and how it's protected (mfa_method) and recovers (recovery_notes as free text, e.g. "recovery
-- email: x@y.com", never the credential itself); the actual secret belongs in a password manager.
CREATE TABLE digital_accounts (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id         uuid REFERENCES people(id) ON DELETE SET NULL,
    service_name      varchar(150) NOT NULL,
    category          varchar(20) NOT NULL DEFAULT 'OTHER'
                          CHECK (category IN ('DEVELOPMENT', 'BANKING', 'GOVERNMENT', 'STREAMING',
                                               'SHOPPING', 'SOCIAL', 'GAMING', 'CLOUD', 'OTHER')),
    email_used        varchar(255),
    mfa_method        varchar(20) NOT NULL DEFAULT 'NONE'
                          CHECK (mfa_method IN ('NONE', 'PASSKEY', 'TOTP', 'SMS', 'APP', 'EMAIL')),
    recovery_notes    text,
    status            varchar(20) NOT NULL DEFAULT 'ACTIVE'
                          CHECK (status IN ('ACTIVE', 'INACTIVE', 'DELETED')),
    last_reviewed_at  timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_digital_accounts_person_id ON digital_accounts (person_id);
-- Backs "which accounts still use my old Gmail" — the query that motivated this table.
CREATE INDEX idx_digital_accounts_email_used ON digital_accounts (email_used);
