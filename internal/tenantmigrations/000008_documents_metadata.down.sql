DROP INDEX idx_documents_expiration_date;

ALTER TABLE documents
    DROP COLUMN description,
    DROP COLUMN institution,
    DROP COLUMN amount,
    DROP COLUMN expiration_date,
    DROP COLUMN external_url,
    DROP COLUMN notes;
