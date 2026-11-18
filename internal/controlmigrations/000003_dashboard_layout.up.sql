-- dashboard_layouts (dono/public) — one row per app user, storing their custom dashboard widget
-- arrangement (drag/resize positions from the "Editar layout" UI mode) as an opaque JSON array.
-- The backend never decodes `layout`'s contents (see internal/dashboardlayout/domain.go); it's
-- presentation state produced and consumed by react-grid-layout on the frontend, not domain data.
-- Same table also created per-tenant in internal/tenantmigrations/000030_dashboard_layout.up.sql
-- (see docs/tenant-self-service.md for why domain-shaped tables still exist here pre-cutover).
CREATE TABLE IF NOT EXISTS dashboard_layouts (
    app_user_id uuid PRIMARY KEY REFERENCES app_users(id) ON DELETE CASCADE,
    layout      jsonb NOT NULL DEFAULT '[]',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- member_permissions.module's CHECK (000001_control_schema.up.sql) is an unnamed inline
-- constraint, so Postgres assigned it the default <table>_<column>_check name.
ALTER TABLE member_permissions DROP CONSTRAINT IF EXISTS member_permissions_module_check;
ALTER TABLE member_permissions ADD CONSTRAINT member_permissions_module_check
    CHECK (module IN ('categories', 'clients', 'loans', 'expenses', 'income', 'assets',
        'documents', 'people', 'personinfo', 'health', 'pets', 'reports', 'notifications',
        'integrations', 'finance', 'dashboardlayout'));
