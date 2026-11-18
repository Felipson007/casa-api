-- Enriches documents with the metadata fields needed to track life-admin records (insurance,
-- warranties, tax deadlines, contracts) that don't route into another module the way
-- invoices/paystubs/lab results do (see internal/documents/extract.go). All nullable — existing
-- rows carry none of this, and most documents (PERSONAL/ASSET/PET/OTHER) will keep them empty.
-- expiration_date is `date`, not `timestamptz` — same reasoning as the existing document_date
-- column (civildate.Date on the Go side): a policy/warranty/tax deadline is a calendar date, not
-- a timezoned instant.
ALTER TABLE documents
    ADD COLUMN description     text        NULL,
    ADD COLUMN institution     varchar(200) NULL,
    ADD COLUMN amount          numeric(12,2) NULL,
    ADD COLUMN expiration_date date        NULL,
    ADD COLUMN external_url    text        NULL,
    ADD COLUMN notes           text        NULL;

-- Powers the "próximos vencimentos" query (documents expiring soon) — see docs/gap-analysis-vida-
-- digital-20260822.md GAP 5.3. Partial index: most rows will never have an expiration_date.
CREATE INDEX idx_documents_expiration_date ON documents (expiration_date)
    WHERE expiration_date IS NOT NULL;
