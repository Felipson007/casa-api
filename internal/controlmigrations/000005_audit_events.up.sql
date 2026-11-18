-- audit_events — control-plane audit trail (security events, application errors and panics,
-- frontend exceptions). Lives in `public` alongside app_users/sessions because it is deliberately
-- cross-tenant: the owner reviews every tenant's failures from one screen. Written asynchronously
-- by internal/auditevents.Recorder (never in the request path) and pruned by the same recorder on
-- a 3-day retention window (AUDIT_RETENTION_DAYS), so the table cannot grow without bound.
--
-- Mirrored in internal/migrations/000035_audit_events.up.sql, which is what CI applies before the
-- integration tests (see .github/workflows/ci.yml).
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
