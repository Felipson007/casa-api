-- category_source records HOW a launch got its category, so a later launch with the same or a
-- similar description can inherit it (internal/categorize) and a human correction always wins:
--   NONE    uncategorised
--   AI      the invoice-extraction model chose it
--   RULE    the Open Finance keyword rules chose it (internal/openfinance/mapping.go)
--   HISTORY inherited from a similar launch's category
--   MANUAL  a human chose or edited it — authoritative, never overwritten by autocategorisation
--
-- Backfill: an existing categorised row keeps the provenance its source implies.
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
