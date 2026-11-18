-- dashboard_layouts — one row per app user, storing their custom dashboard widget arrangement as
-- an opaque JSON array (see internal/migrations/000033_dashboard_layout.up.sql for the same table
-- in the control schema, and internal/dashboardlayout/domain.go for why the payload stays opaque).
-- member_permissions is a control-plane table (public schema only, see
-- internal/tenantmigrations/000001_init_schema.up.sql) — this migration doesn't touch it.
CREATE TABLE dashboard_layouts (
    app_user_id uuid PRIMARY KEY REFERENCES public.app_users(id) ON DELETE CASCADE,
    layout      jsonb NOT NULL DEFAULT '[]',
    updated_at  timestamptz NOT NULL DEFAULT now()
);
