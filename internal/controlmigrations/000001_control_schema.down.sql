DROP INDEX IF EXISTS idx_app_users_tenant_id;
ALTER TABLE app_users DROP COLUMN IF EXISTS tenant_id;
DROP TABLE IF EXISTS tenants;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS password_reset_tokens;
DROP TABLE IF EXISTS member_permissions;
DROP TABLE IF EXISTS app_users;
