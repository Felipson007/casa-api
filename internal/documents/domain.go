// Package documents is the file-metadata module (documentos in the old app) — owned by Claude per
// decisions/DECISIONS.md D-005. References people/assets/pets, so it's built last among those
// three (needs assets+pets to already exist).
package documents

import (
	"time"

	"github.com/google/uuid"
)

// FileType mirrors the documents.file_type CHECK constraint.
type FileType string

const (
	FileTypePDF   FileType = "PDF"
	FileTypePNG   FileType = "PNG"
	FileTypeJPEG  FileType = "JPEG"
	FileTypeWEBP  FileType = "WEBP"
	FileTypeOther FileType = "OTHER"
)

// Origin mirrors the old Java DocumentoOrigem enum (FATURA/HOLERITE/EXAME/PESSOAL/BEM/PET/OUTRO,
// translated to English per the naming convention) — confirmed with Hermes, who read the old
// codebase directly, since the documents.origin column has no CHECK constraint in the schema
// (unlike FileType). Validated as a closed set here at the Go layer (DecodeAndValidate's
// "oneof" tag on dto.go, same idiom as every other enum-like field in this codebase) rather than
// left as a free string, and rather than requesting a DB CHECK be added (would need a
// migrations change, Hermes-owned per D-005 — not necessary since Go-level validation already
// gives the same safety for anything going through this module's own Create/Update).
//
// LAB_RESULT (EXAME) is the value the upcoming health module will care about — a document with
// that origin is expected to be an exam/lab result, chaining into health_records.
type Origin string

// ExtractionStatus is the outcome of the itemized auto-extraction + reconciliation pass for an
// INVOICE/PAYSTUB document (documents.extraction_status, migration 000013). It is nil for
// documents whose origin has no itemized extraction (everything except INVOICE/PAYSTUB) and for
// rows imported before 000013.
type ExtractionStatus = string

const (
	// ExtractionPending — extraction has not finished yet (set the moment Extract starts).
	ExtractionPending ExtractionStatus = "PENDING"
	// ExtractionReconciled — the line items sum to the statement's printed total (or the printed
	// total could not be read, so there is nothing to reconcile against).
	ExtractionReconciled ExtractionStatus = "RECONCILED"
	// ExtractionAdjusted — the user (or the assistant) confirmed the correct total via Reconcile
	// and a single labelled adjustment line was added for the residual so the competência month
	// matches the statement.
	ExtractionAdjusted ExtractionStatus = "ADJUSTED"
	// ExtractionNeedsInput — auto-extraction could not make the lines sum to the printed total;
	// the document is parked in the Inbox with a question for the user (see ExtractionNote).
	ExtractionNeedsInput ExtractionStatus = "NEEDS_INPUT"
	// ExtractionFailed — the model returned nothing usable (no line items at all).
	ExtractionFailed ExtractionStatus = "FAILED"
	// ExtractionAwaitingReview — the model returned usable line items, but nothing has been
	// written to expenses/income/health_records yet: the itemized result sits in
	// Document.PendingExtraction until the user reviews it (editing fields if needed) and calls
	// POST /api/documents/{id}/confirm-extraction. Reconciliation (does the sum match the printed
	// total?) still runs at extraction time — Delta/Note are populated here too, so the review UI
	// can show the "doesn't add up" warning immediately without recomputing it — but that verdict
	// no longer gates persistence the way NEEDS_INPUT used to; it's advisory until confirmed.
	ExtractionAwaitingReview ExtractionStatus = "AWAITING_REVIEW"
)

// TaxDocKind narrows an Origin=TAX document to which of the fixed monthly accounting deliverables
// (the same 5 documents the accountant sends every month for a PJ) it is — backs a "N/5 sent this
// month" checklist in the frontend's Fiscal page. Nil for TAX documents uploaded before this field
// existed, and for every non-TAX origin. No DB CHECK constraint (migration
// 000018_documents_tax_doc_kind.up.sql) — validated here at the Go layer, same idiom as Origin.
type TaxDocKind string

const (
	TaxDocDAS            TaxDocKind = "DAS"
	TaxDocDASDeclaration TaxDocKind = "DAS_DECLARATION"
	TaxDocNFSe           TaxDocKind = "NFSE"
	TaxDocFederalTaxes   TaxDocKind = "FEDERAL_TAXES"
	TaxDocMonthlyClosing TaxDocKind = "MONTHLY_CLOSING"
	TaxDocOther          TaxDocKind = "OTHER"
)

const (
	OriginInvoice   Origin = "INVOICE"    // FATURA
	OriginPaystub   Origin = "PAYSTUB"    // HOLERITE
	OriginLabResult Origin = "LAB_RESULT" // EXAME
	OriginPersonal  Origin = "PERSONAL"   // PESSOAL
	OriginAsset     Origin = "ASSET"      // BEM
	OriginPet       Origin = "PET"        // PET
	OriginInsurance Origin = "INSURANCE"  // no Java counterpart — added 2026-08-22, life-OS gap analysis
	OriginWarranty  Origin = "WARRANTY"   // no Java counterpart — added 2026-08-22, life-OS gap analysis
	OriginTax       Origin = "TAX"        // no Java counterpart — added 2026-08-22, life-OS gap analysis
	OriginContract  Origin = "CONTRACT"   // CONTRATO
	// OriginResume — a filed résumé/CV, optionally linked from a jobexperience.Experience row
	// (ResumeDocumentID). Deliberately excluded from Service.Extract's origin switch — no
	// auto-extraction runs on it, it's filed purely for reference.
	OriginResume Origin = "RESUME"
	OriginOther  Origin = "OTHER" // OUTRO
)

// Document mirrors the documents table (internal/migrations/000001_init_schema.up.sql,
// widened by 000008_documents_metadata.up.sql). PersonID/AssetID/PetID are nullable FKs with ON
// DELETE SET NULL. Description/Institution/Amount/ExpirationDate/ExternalURL/Notes are the
// life-admin metadata fields (insurance policies, warranties, tax deadlines, contracts) added
// 2026-08-22 — all nullable, since most documents (invoices/paystubs/lab results, which route into
// expenses/income/health via Extract) never set them.
type Document struct {
	ID               uuid.UUID
	PersonID         *uuid.UUID
	OriginalFilename string
	FilePath         string
	FileType         FileType
	Origin           Origin
	DocumentDate     *time.Time
	Tags             *string
	AssetID          *uuid.UUID
	PetID            *uuid.UUID
	Description      *string
	Institution      *string
	Amount           *string
	ExpirationDate   *time.Time
	ExternalURL      *string
	Notes            *string
	CreatedAt        time.Time

	// Itemized-extraction reconciliation state (migration 000013) — only ever set for
	// INVOICE/PAYSTUB, written by Service.Extract / Service.Reconcile, never by the generic
	// Create/Update path. StatementTotal/ExtractionDelta are numeric(15,2)-as-string like every
	// other money column. StatementClosingDate is the resolved competência (always day 1).
	StatementTotal       *string
	StatementClosingDate *time.Time
	ExtractionStatus     *string
	ExtractionDelta      *string
	ExtractionNote       *string

	// PendingExtraction (migration 000021) holds the raw JSON of a PendingExtraction envelope
	// while ExtractionStatus == AWAITING_REVIEW — cleared once POST .../confirm-extraction
	// persists it into expenses/income/health_records. Nil the rest of the time.
	PendingExtraction *string

	// ContentHash is the SHA-256 of the uploaded bytes (migration 000014) — how a re-upload of
	// the same file is recognised as the same document instead of becoming a second one. Nil for
	// rows created before that migration and for metadata-only documents (no file).
	ContentHash *string

	// TaxDocKind narrows an Origin=TAX document (migration 000018) — see its type doc comment.
	TaxDocKind *TaxDocKind
}

// ExtractionResult is what Service.Extract / Service.Reconcile persist about one INVOICE/PAYSTUB
// document's itemized extraction — the "did it reconcile with the printed total?" verdict, kept
// separate from the generic document metadata so repo.UpdateExtraction can touch only these
// columns.
type ExtractionResult struct {
	StatementTotal       *string
	StatementClosingDate *time.Time
	Status               ExtractionStatus
	Delta                *string
	Note                 *string
	// PendingExtraction, ClearPendingExtraction: exactly one non-zero-value case applies per call.
	// Landing on AWAITING_REVIEW sets PendingExtraction (the JSON to stage). Confirming (or a
	// terminal FAILED/RECONCILED that never had a draft to begin with) sets ClearPendingExtraction
	// so repo.UpdateExtraction NULLs the column instead of leaving a stale draft behind once it's
	// been consumed.
	PendingExtraction      *string
	ClearPendingExtraction bool
}
