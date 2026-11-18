-- One-time cutover: move the owner's domain tables out of `public` into a dedicated schema
-- (`tenant_owner`), leaving only the control-plane tables behind.
--
-- RUN INSIDE A SINGLE TRANSACTION, WITH atlas-api AND atlas-worker STOPPED. Back up first
-- (`pg_dump -F c`). See docs/tenant-self-service.md for the full runbook.
--
-- Scope of this file is intentionally the concrete owner cutover (schema `tenant_owner`, slug
-- `owner`); adjust the two literals if the slug differs.
\set ON_ERROR_STOP on

BEGIN;

CREATE SCHEMA IF NOT EXISTS tenant_owner;

-- Move every domain table. Control tables and the legacy migration table stay in public, as do the
-- ad-hoc *_bkp_* archive tables (not part of the app schema).
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT c.relname
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'public'
          AND c.relkind = 'r'
          AND c.relname NOT IN (
              'app_users', 'member_permissions', 'password_reset_tokens', 'sessions',
              'tenants', 'schema_migrations',
              'expenses_bkp_invoice_unlinked_20260920',
              'expenses_bkp_pre_align_20260920',
              'income_entries_bkp_pre_clean_20260920'
          )
    LOOP
        EXECUTE format('ALTER TABLE public.%I SET SCHEMA tenant_owner', r.relname);
        RAISE NOTICE 'moved table %', r.relname;
    END LOOP;
END $$;

-- Move the person-relationship view (its base tables moved above).
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT c.relname
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'public' AND c.relkind = 'v'
    LOOP
        EXECUTE format('ALTER VIEW public.%I SET SCHEMA tenant_owner', r.relname);
        RAISE NOTICE 'moved view %', r.relname;
    END LOOP;
END $$;

-- Move the one user-defined function (trigger bodies follow their tables; the function resolves
-- table names via search_path at runtime).
ALTER FUNCTION public.bank_projection_deleted() SET SCHEMA tenant_owner;

-- Seed the tenant migration version so the app's boot sweep treats tenant_owner as up to date
-- (the domain schema is the net effect of tenantmigrations up to version 28).
CREATE TABLE IF NOT EXISTS tenant_owner.schema_migrations (
    version bigint  NOT NULL PRIMARY KEY,
    dirty   boolean NOT NULL
);
INSERT INTO tenant_owner.schema_migrations (version, dirty)
VALUES (28, false)
ON CONFLICT (version) DO NOTHING;

-- Point the owner's tenant at its new schema. The API resolves this per request, so the switch is
-- effective the moment this transaction commits.
UPDATE tenants SET schema_name = 'tenant_owner' WHERE slug = 'owner';

COMMIT;
