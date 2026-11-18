-- Pandora AI Gateway connection settings, stored as a singleton row so operators configure the
-- gateway (URL + Admin/Client API keys) once in the Atlas UI instead of pasting keys into the
-- browser bundle. The atlas-go proxy reads this row and injects the right key per route.
CREATE TABLE pandora_config (
    id          boolean PRIMARY KEY DEFAULT true,  -- singleton: exactly one row (id = true)
    gateway_url text NOT NULL DEFAULT '',
    admin_key   text NOT NULL DEFAULT '',
    client_key  text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

INSERT INTO pandora_config (id) VALUES (true);
