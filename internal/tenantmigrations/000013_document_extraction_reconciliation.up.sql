-- ============================================================================================
-- Document extraction reconciliation — invoice/paystub auto-extraction (documents.Service.Extract)
-- now itemizes the whole statement, normalizes every line to the closing/reference month
-- (competência = day 1), and checks the line sum against the printed total. These columns record
-- the outcome so the Inbox can ask the user for the correct total when it can't reconcile on its
-- own, and so a later integrity check can flag a statement whose lines drifted from its total.
-- ============================================================================================
ALTER TABLE documents
    ADD COLUMN statement_total        numeric(15,2),
    ADD COLUMN statement_closing_date date,
    ADD COLUMN extraction_status      varchar(20),
    ADD COLUMN extraction_delta       numeric(15,2),
    ADD COLUMN extraction_note        text;

-- Partial index: the Inbox query is "every document still waiting on a human answer".
CREATE INDEX idx_documents_extraction_needs_input
    ON documents (created_at DESC)
    WHERE extraction_status = 'NEEDS_INPUT';
