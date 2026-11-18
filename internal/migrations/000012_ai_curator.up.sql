CREATE TABLE ai_curator_runs (
    run_date date PRIMARY KEY,
    completed_at timestamptz NOT NULL DEFAULT now(),
    reclassified_count integer NOT NULL DEFAULT 0,
    insight_count integer NOT NULL DEFAULT 0
);

CREATE TABLE ai_insights (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type varchar(32) NOT NULL,
    severity varchar(16) NOT NULL,
    title varchar(160) NOT NULL,
    message text NOT NULL,
    generated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_ai_insights_type CHECK (type IN ('FINANCIAL', 'HEALTH', 'ORGANIZATION')),
    CONSTRAINT ck_ai_insights_severity CHECK (severity IN ('INFO', 'WARNING', 'CRITICAL'))
);

CREATE INDEX idx_ai_insights_generated_at ON ai_insights (generated_at DESC);
