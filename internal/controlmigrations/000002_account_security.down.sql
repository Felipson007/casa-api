DROP TABLE IF EXISTS mfa_challenges;
DROP TABLE IF EXISTS mfa_recovery_codes;

ALTER TABLE app_users DROP COLUMN IF EXISTS mfa_enabled_at;
ALTER TABLE app_users DROP COLUMN IF EXISTS mfa_enabled;
ALTER TABLE app_users DROP COLUMN IF EXISTS mfa_secret;
ALTER TABLE app_users DROP COLUMN IF EXISTS last_name;
ALTER TABLE app_users DROP COLUMN IF EXISTS first_name;
