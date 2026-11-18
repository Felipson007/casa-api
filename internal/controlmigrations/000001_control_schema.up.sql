-- Control-plane schema (docs/tenant-self-service.md): the tables that live in `public` and are
-- shared by every tenant. Deliberately idempotent (IF NOT EXISTS) so this can be applied to a
-- database that already had these tables created by the pre-split `internal/migrations` history.
--
-- Tracked in its own `schema_migrations_control` table (see cmd/api/main.go), separate from the
-- legacy `schema_migrations` and from each tenant schema's own migration table, so the control and
-- tenant migration streams evolve independently.
CREATE TABLE IF NOT EXISTS app_users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         varchar(255) NOT NULL,
    role          varchar(20) NOT NULL CHECK (role IN ('OWNER', 'MEMBER')),
    password_hash varchar(255),
    name          varchar(255),
    last_login_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_app_users_email ON app_users (email);

CREATE TABLE IF NOT EXISTS member_permissions (
    user_id    uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    module     varchar(30) NOT NULL CHECK (module IN ('categories', 'clients', 'loans', 'expenses',
        'income', 'assets', 'documents', 'people', 'personinfo', 'health', 'pets', 'reports',
        'notifications', 'integrations', 'finance')),
    can_read   boolean NOT NULL DEFAULT true,
    can_write  boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, module)
);

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_user_id uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    token_hash  varchar(255) NOT NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_password_reset_tokens_token_hash ON password_reset_tokens (token_hash);

CREATE TABLE IF NOT EXISTS sessions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_user_id uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    token_hash  varchar(255) NOT NULL,
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_token_hash ON sessions (token_hash);
CREATE INDEX IF NOT EXISTS idx_sessions_app_user_id ON sessions (app_user_id);

-- Tenant registry. schema_name is the Postgres schema holding that tenant's domain tables; the
-- default tenant (the owner, pre-cutover) uses `public`.
CREATE TABLE IF NOT EXISTS tenants (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug         varchar(40) NOT NULL,
    schema_name  varchar(63) NOT NULL,
    display_name varchar(255),
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_slug ON tenants (slug);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_schema_name ON tenants (schema_name);

-- Envelope encryption: wrapped per-tenant DEK (master KEK lives outside the DB).
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS dek_wrapped bytea;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS key_version integer NOT NULL DEFAULT 1;

-- Binds a user to the tenant whose schema their domain queries resolve to.
ALTER TABLE app_users ADD COLUMN IF NOT EXISTS tenant_id uuid REFERENCES tenants(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_app_users_tenant_id ON app_users (tenant_id);
