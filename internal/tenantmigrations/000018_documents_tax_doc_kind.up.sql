-- ============================================================================================
-- documents_tax_doc_kind — narrows an Origin=TAX document to which of the fixed monthly
-- accounting deliverables it is (DAS, DAS declaration, NFS-e, federal taxes, monthly closing, or
-- other), backing a "N/5 documentos enviados este mês" checklist in the frontend's Fiscal page.
-- No CHECK constraint — validated at the Go layer (documents.TaxDocKind), same pattern already
-- used for the `origin` column itself.
-- ============================================================================================
ALTER TABLE documents ADD COLUMN tax_doc_kind varchar(30);
