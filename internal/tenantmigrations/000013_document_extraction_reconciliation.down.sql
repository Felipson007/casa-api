DROP INDEX IF EXISTS idx_documents_extraction_needs_input;

ALTER TABLE documents
    DROP COLUMN IF EXISTS statement_total,
    DROP COLUMN IF EXISTS statement_closing_date,
    DROP COLUMN IF EXISTS extraction_status,
    DROP COLUMN IF EXISTS extraction_delta,
    DROP COLUMN IF EXISTS extraction_note;
