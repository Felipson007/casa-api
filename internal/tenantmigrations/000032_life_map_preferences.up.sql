-- life_map_preferences — one row per app user, storing which Life Map area ids they've chosen to
-- hide from the "Mapa da Vida" card on the Dashboard, as a plain JSON array of area id strings (see
-- internal/controlmigrations/000004_life_map_preferences.up.sql for the same table in the control
-- schema, and internal/lifemapprefs/domain.go for why ids aren't validated against a known set).
-- member_permissions is a control-plane table (public schema only, see
-- internal/tenantmigrations/000001_init_schema.up.sql) — this migration doesn't touch it.
CREATE TABLE life_map_preferences (
    app_user_id  uuid PRIMARY KEY REFERENCES public.app_users(id) ON DELETE CASCADE,
    hidden_areas jsonb NOT NULL DEFAULT '[]',
    updated_at   timestamptz NOT NULL DEFAULT now()
);
