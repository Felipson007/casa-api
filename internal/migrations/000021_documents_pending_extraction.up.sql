-- ============================================================================================
-- documents_pending_extraction — the Inbox review checkpoint. Extraction (extract.go) used to
-- write straight into expenses/income/health_records; it now serializes the itemized result into
-- this column and stops at AWAITING_REVIEW, so nothing lands in the user's financial data until
-- they've reviewed (and possibly corrected) it via POST /api/documents/{id}/confirm-extraction.
-- No CHECK constraint — same pattern as extraction_status (000013) and tax_doc_kind (000018),
-- validated at the Go layer against the FaturaExtraida/HoleriteExtraido/ExameExtraido schemas.
-- ============================================================================================
ALTER TABLE documents ADD COLUMN pending_extraction jsonb;
