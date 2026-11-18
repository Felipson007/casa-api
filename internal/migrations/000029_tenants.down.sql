DROP INDEX IF EXISTS idx_app_users_tenant_id;
ALTER TABLE app_users DROP COLUMN IF EXISTS tenant_id;
DROP TABLE IF EXISTS tenants;
