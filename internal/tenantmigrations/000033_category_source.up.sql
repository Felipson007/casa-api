-- Same column/backfill as internal/migrations/000035_category_source.up.sql, mirrored into the
-- per-tenant schema baseline (see internal/tenantmigrations/embed.go for why the two trees exist).
ALTER TABLE expenses
    ADD COLUMN category_source varchar(10) NOT NULL DEFAULT 'NONE'
        CHECK (category_source IN ('NONE', 'AI', 'RULE', 'HISTORY', 'MANUAL'));
UPDATE expenses
   SET category_source = CASE source
           WHEN 'PLUGGY'  THEN 'RULE'
           WHEN 'DOCUMENT' THEN 'AI'
           ELSE 'MANUAL'
       END
 WHERE category_id IS NOT NULL;

ALTER TABLE income_entries
    ADD COLUMN category_source varchar(10) NOT NULL DEFAULT 'NONE'
        CHECK (category_source IN ('NONE', 'AI', 'RULE', 'HISTORY', 'MANUAL'));
UPDATE income_entries
   SET category_source = CASE source
           WHEN 'PLUGGY'  THEN 'RULE'
           WHEN 'DOCUMENT' THEN 'AI'
           ELSE 'MANUAL'
       END
 WHERE category_id IS NOT NULL;
