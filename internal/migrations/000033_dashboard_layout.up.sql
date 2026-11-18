-- Ports internal/controlmigrations/000003_dashboard_layout.up.sql into internal/migrations, whose
-- promise (docs/tenant-self-service.md "Estado incremental") is to stay in sync with the
-- control-plane migrations until the owner's cutover: `public` is still both control AND the
-- owner's domain schema today, and internal/migrations is what actually runs against it in
-- production and in CI (see .github/workflows/ci.yml, "Apply migrations for integration tests" —
-- account_security/000030 drifted the same way before this, see that file's own header comment).
--
-- dashboard_layouts — one row per app user, storing their custom dashboard widget arrangement
-- (drag/resize positions from the "Editar layout" UI mode) as an opaque JSON array. The backend
-- never decodes `layout`'s contents (see internal/dashboardlayout/domain.go); it's presentation
-- state produced and consumed by react-grid-layout on the frontend, not domain data.
CREATE TABLE dashboard_layouts (
    app_user_id uuid PRIMARY KEY REFERENCES app_users(id) ON DELETE CASCADE,
    layout      jsonb NOT NULL DEFAULT '[]',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- member_permissions.module's CHECK (000001_init_schema.up.sql) is an unnamed inline constraint,
-- so Postgres assigned it the default <table>_<column>_check name.
ALTER TABLE member_permissions DROP CONSTRAINT member_permissions_module_check;
ALTER TABLE member_permissions ADD CONSTRAINT member_permissions_module_check
    CHECK (module IN ('categories', 'clients', 'loans', 'expenses', 'income', 'assets',
        'documents', 'people', 'personinfo', 'health', 'pets', 'reports', 'notifications',
        'integrations', 'finance', 'dashboardlayout'));
