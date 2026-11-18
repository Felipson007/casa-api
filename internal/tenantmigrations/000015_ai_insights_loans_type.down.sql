-- Revert to the 000012 CHECK. LOANS rows would violate it, so drop them first.
DELETE FROM ai_insights WHERE type = 'LOANS';
ALTER TABLE ai_insights DROP CONSTRAINT ck_ai_insights_type;
ALTER TABLE ai_insights
    ADD CONSTRAINT ck_ai_insights_type CHECK (type IN ('FINANCIAL', 'HEALTH', 'ORGANIZATION'));
