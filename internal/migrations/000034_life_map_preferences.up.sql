-- Ports internal/controlmigrations/000004_life_map_preferences.up.sql into internal/migrations,
-- whose promise (docs/tenant-self-service.md "Estado incremental") is to stay in sync with the
-- control-plane migrations until the owner's cutover: `public` is still both control AND the
-- owner's domain schema today, and internal/migrations is what actually runs against it in
-- production and in CI (see .github/workflows/ci.yml, "Apply migrations for integration tests" —
-- account_security/000030 and dashboard_layout/000033 drifted the same way before this).
--
-- life_map_preferences — one row per app user, storing which Life Map area ids (Identidade,
-- Finanças, Saúde, Carreira, etc. — see the frontend's useDashboardPessoal.ts) they've chosen to
-- hide from the "Mapa da Vida" card on the Dashboard, as a plain JSON array of area id strings. The
-- backend never validates ids against a known set (see internal/lifemapprefs/domain.go): an
-- unknown/removed id just harmlessly stops matching anything on the frontend.
CREATE TABLE life_map_preferences (
    app_user_id  uuid PRIMARY KEY REFERENCES app_users(id) ON DELETE CASCADE,
    hidden_areas jsonb NOT NULL DEFAULT '[]',
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- member_permissions.module's CHECK (000001_init_schema.up.sql) is an unnamed inline constraint,
-- so Postgres assigned it the default <table>_<column>_check name.
ALTER TABLE member_permissions DROP CONSTRAINT member_permissions_module_check;
ALTER TABLE member_permissions ADD CONSTRAINT member_permissions_module_check
    CHECK (module IN ('categories', 'clients', 'loans', 'expenses', 'income', 'assets',
        'documents', 'people', 'personinfo', 'health', 'pets', 'reports', 'notifications',
        'integrations', 'finance', 'dashboardlayout', 'lifemapprefs'));
