-- Self-service account security: structured name fields + optional TOTP MFA.
--
-- first_name/last_name are the structured replacement for the single `name` column (which stays as
-- the denormalized display name so every existing reader keeps working). mfa_secret stores the
-- TOTP shared secret ENCRYPTED (AES-256-GCM via APP_ENCRYPTION_KEY) — a database dump alone must
-- not be enough to mint valid MFA codes. Recovery codes are stored only as SHA-256 hashes.
ALTER TABLE app_users ADD COLUMN IF NOT EXISTS first_name varchar(100);
ALTER TABLE app_users ADD COLUMN IF NOT EXISTS last_name varchar(100);
ALTER TABLE app_users ADD COLUMN IF NOT EXISTS mfa_secret bytea;
ALTER TABLE app_users ADD COLUMN IF NOT EXISTS mfa_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE app_users ADD COLUMN IF NOT EXISTS mfa_enabled_at timestamptz;

-- Single-use MFA recovery codes. High-entropy codes, so a deterministic SHA-256 hash is enough
-- (same reasoning as sessions/password_reset_tokens): it makes lookup-by-equality possible.
CREATE TABLE IF NOT EXISTS mfa_recovery_codes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_user_id uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    code_hash   varchar(255) NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_mfa_recovery_codes_user ON mfa_recovery_codes (app_user_id);

-- Short-lived handoff between "password was correct" and "second factor was verified". Only the
-- hash is stored; a challenge is single-use and expires in minutes (see auth.mfaChallengeValidity).
CREATE TABLE IF NOT EXISTS mfa_challenges (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_user_id uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    token_hash  varchar(255) NOT NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mfa_challenges_token_hash ON mfa_challenges (token_hash);
CREATE INDEX IF NOT EXISTS idx_mfa_challenges_user ON mfa_challenges (app_user_id);
