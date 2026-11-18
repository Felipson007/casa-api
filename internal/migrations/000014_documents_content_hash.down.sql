DROP INDEX IF EXISTS idx_documents_content_hash;
ALTER TABLE documents DROP COLUMN IF EXISTS content_hash;
