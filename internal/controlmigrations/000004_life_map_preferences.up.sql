-- life_map_preferences (dono/public) — one row per app user, storing which Life Map area ids
-- (Identidade, Finanças, Saúde, Carreira, etc. — see the frontend's useDashboardPessoal.ts) they've
-- chosen to hide from the "Mapa da Vida" card on the Dashboard. hidden_areas is a plain JSON array
-- of area id strings — the backend doesn't validate them against a known set (see
-- internal/lifemapprefs/domain.go): an unknown/removed id just harmlessly stops matching anything
-- on the frontend. Same table also created per-tenant in
-- internal/tenantmigrations/000032_life_map_preferences.up.sql (see docs/tenant-self-service.md
-- for why domain-shaped tables still exist here pre-cutover).
CREATE TABLE IF NOT EXISTS life_map_preferences (
    app_user_id  uuid PRIMARY KEY REFERENCES app_users(id) ON DELETE CASCADE,
    hidden_areas jsonb NOT NULL DEFAULT '[]',
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- member_permissions.module's CHECK (000001_control_schema.up.sql) is an unnamed inline
-- constraint, so Postgres assigned it the default <table>_<column>_check name.
ALTER TABLE member_permissions DROP CONSTRAINT IF EXISTS member_permissions_module_check;
ALTER TABLE member_permissions ADD CONSTRAINT member_permissions_module_check
    CHECK (module IN ('categories', 'clients', 'loans', 'expenses', 'income', 'assets',
        'documents', 'people', 'personinfo', 'health', 'pets', 'reports', 'notifications',
        'integrations', 'finance', 'dashboardlayout', 'lifemapprefs'));
