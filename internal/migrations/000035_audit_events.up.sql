-- audit_events — same table as internal/controlmigrations/000005_audit_events.up.sql (control
-- schema). Duplicated here because CI applies internal/migrations directly to the test database
-- before the integration tests, while production applies controlmigrations on boot. The two sets
-- are never applied to the same database, so CREATE TABLE IF NOT EXISTS keeps both paths correct.
CREATE TABLE IF NOT EXISTS audit_events (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type  varchar(40) NOT NULL,
    severity    varchar(10) NOT NULL DEFAULT 'info',
    user_id     uuid REFERENCES app_users(id) ON DELETE SET NULL,
    user_email  varchar(255),
    tenant_id   uuid,
    ip          varchar(64),
    user_agent  varchar(512),
    method      varchar(10),
    path        varchar(512),
    status_code integer,
    request_id  varchar(64),
    message     text,
    detail      text,
    metadata    jsonb NOT NULL DEFAULT '{}'::jsonb,
    duration_ms integer,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_events_created_at ON audit_events (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_type_created ON audit_events (event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_severity_created ON audit_events (severity, created_at DESC);
