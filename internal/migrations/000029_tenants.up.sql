-- tenants — registry of the schema-per-tenant model (docs/tenant-self-service.md). Control-plane
-- table: it lives in `public`, alongside app_users/sessions, never in a tenant schema.
CREATE TABLE tenants (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug         varchar(40) NOT NULL,
    schema_name  varchar(63) NOT NULL,
    display_name varchar(255),
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_tenants_slug ON tenants (slug);
CREATE UNIQUE INDEX idx_tenants_schema_name ON tenants (schema_name);

-- app_users.tenant_id binds a user to the tenant whose schema their domain queries resolve to.
-- Nullable so the migration is additive and safe on an existing deployment; the app backfills the
-- owner's users to the default tenant at boot (tenancy.EnsureDefaultTenant).
ALTER TABLE app_users ADD COLUMN tenant_id uuid REFERENCES tenants(id) ON DELETE SET NULL;

CREATE INDEX idx_app_users_tenant_id ON app_users (tenant_id);

-- Envelope encryption (docs/tenant-self-service.md): each tenant's data-encryption key (DEK) is
-- stored here wrapped by the master KEK, which lives OUTSIDE the database (TENANT_MASTER_KEY). A
-- dump/backup alone therefore cannot decrypt tenant data; destroying dek_wrapped crypto-shreds it.
ALTER TABLE tenants ADD COLUMN dek_wrapped bytea;
ALTER TABLE tenants ADD COLUMN key_version integer NOT NULL DEFAULT 1;
