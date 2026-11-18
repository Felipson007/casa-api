-- ============================================================================================
-- documents.content_hash — SHA-256 of the uploaded bytes, so re-uploading the same file is
-- recognised instead of creating a second document.
--
-- Why: file_path is a random hex name (LocalStorage.Store), so nothing tied two uploads of the
-- same PDF together. Uploading one invoice twice produced two documents, each extracted into its
-- own full set of expense lines, and the competência month reported double (2026-09 showed
-- R$ 19.980,68 for a R$ 9.996,79 statement). Nullable: rows imported before this migration have
-- no hash and simply never match.
-- ============================================================================================
ALTER TABLE documents ADD COLUMN content_hash char(64);

CREATE INDEX idx_documents_content_hash ON documents (content_hash) WHERE content_hash IS NOT NULL;
