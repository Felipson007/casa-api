package documents

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// DocumentDTO is the JSON shape returned to clients. DocumentDate/ExpirationDate are
// civildate.Date, not time.Time — see that type's doc comment. Description/Institution/Amount/
// ExternalURL/Notes are the life-admin metadata fields added 2026-08-22 (docs/gap-analysis-vida-
// digital-20260822.md GAP 2).
type DocumentDTO struct {
	ID               uuid.UUID  `json:"id"`
	PersonID         *uuid.UUID `json:"personId,omitempty"`
	OriginalFilename string     `json:"originalFilename"`
	// HasFile replaces the old raw FilePath field: the frontend only ever needed "is there a
	// downloadable file?" (temArquivo), and exposing the real on-disk path was a needless
	// information leak. True for locally-stored and Google-Drive-backed documents, false for
	// metadata-only records.
	HasFile        bool            `json:"hasFile"`
	FileType       FileType        `json:"fileType"`
	Origin         Origin          `json:"origin"`
	DocumentDate   *civildate.Date `json:"documentDate,omitempty"`
	Tags           *string         `json:"tags,omitempty"`
	AssetID        *uuid.UUID      `json:"assetId,omitempty"`
	PetID          *uuid.UUID      `json:"petId,omitempty"`
	Description    *string         `json:"description,omitempty"`
	Institution    *string         `json:"institution,omitempty"`
	Amount         *string         `json:"amount,omitempty"`
	ExpirationDate *civildate.Date `json:"expirationDate,omitempty"`
	ExternalURL    *string         `json:"externalUrl,omitempty"`
	Notes          *string         `json:"notes,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`

	// Itemized-extraction reconciliation state (INVOICE/PAYSTUB only, migration 000013). The
	// frontend Inbox renders a "informe o valor correto" card when ExtractionStatus is
	// NEEDS_INPUT, using ExtractionNote as the question text.
	StatementTotal       *string         `json:"statementTotal,omitempty"`
	StatementClosingDate *civildate.Date `json:"statementClosingDate,omitempty"`
	ExtractionStatus     *string         `json:"extractionStatus,omitempty"`
	ExtractionDelta      *string         `json:"extractionDelta,omitempty"`
	ExtractionNote       *string         `json:"extractionNote,omitempty"`
	// PendingExtraction is set only while ExtractionStatus is AWAITING_REVIEW — the Inbox's
	// review dialog renders it, the (possibly edited) same shape is POSTed back to
	// confirm-extraction. Decoded from the stored JSON here rather than left as a string so
	// clients get real JSON, not a JSON-encoded string.
	PendingExtraction *PendingExtraction `json:"pendingExtraction,omitempty"`

	// TaxDocKind narrows an Origin=TAX document — see the type's doc comment in domain.go.
	TaxDocKind *TaxDocKind `json:"taxDocKind,omitempty"`
}

// CreateDocumentRequest is the JSON body accepted by POST /api/documents — a metadata-only
// record (a manually entered contract/insurance policy with no uploaded file). It deliberately
// does NOT take a file path: the server stores a fixed "metadata://" sentinel. A caller-supplied
// path used to be accepted here and flow straight into os.ReadFile at GET
// /api/documents/{id}/file, i.e. arbitrary local file read. Real files come in through
// POST /api/documents/upload, which derives the path itself.
// INSURANCE/WARRANTY/TAX (added 2026-08-22) have no CHECK constraint in the schema — Origin is a
// closed set validated here at the Go layer, same as every value that came before them (see
// Origin's doc comment). ExternalURL is validated http/https-only (it is rendered as an <a href>
// by the frontend — a "javascript:" value would be stored XSS).
type CreateDocumentRequest struct {
	PersonID         *uuid.UUID      `json:"personId"`
	OriginalFilename string          `json:"originalFilename" validate:"required"`
	FileType         FileType        `json:"fileType" validate:"required,oneof=PDF PNG JPEG WEBP OTHER"`
	Origin           Origin          `json:"origin" validate:"required,oneof=INVOICE PAYSTUB LAB_RESULT PERSONAL ASSET PET INSURANCE WARRANTY TAX CONTRACT RESUME OTHER"`
	DocumentDate     *civildate.Date `json:"documentDate"`
	Tags             *string         `json:"tags"`
	AssetID          *uuid.UUID      `json:"assetId"`
	PetID            *uuid.UUID      `json:"petId"`
	Description      *string         `json:"description"`
	Institution      *string         `json:"institution" validate:"omitempty,max=200"`
	Amount           *string         `json:"amount"`
	ExpirationDate   *civildate.Date `json:"expirationDate"`
	ExternalURL      *string         `json:"externalUrl" validate:"omitempty,http_url"`
	Notes            *string         `json:"notes"`
	// TaxDocKind is only meaningful when Origin is TAX — see the type's doc comment in domain.go.
	TaxDocKind *TaxDocKind `json:"taxDocKind" validate:"omitempty,oneof=DAS DAS_DECLARATION NFSE FEDERAL_TAXES MONTHLY_CLOSING OTHER"`
}

// UpdateDocumentRequest is the JSON body accepted by PUT /api/documents/{id}. Editable fields are
// the metadata/links (tags, person/asset/pet association, document date, and the life-admin
// fields added 2026-08-22) — FilePath/FileType are NOT re-settable here, since changing them out
// from under an already-stored file would desync the metadata from the actual bytes on disk;
// replacing a document's file is a delete+create.
type UpdateDocumentRequest struct {
	PersonID       *uuid.UUID      `json:"personId"`
	DocumentDate   *civildate.Date `json:"documentDate"`
	Tags           *string         `json:"tags"`
	AssetID        *uuid.UUID      `json:"assetId"`
	PetID          *uuid.UUID      `json:"petId"`
	Description    *string         `json:"description"`
	Institution    *string         `json:"institution" validate:"omitempty,max=200"`
	Amount         *string         `json:"amount"`
	ExpirationDate *civildate.Date `json:"expirationDate"`
	ExternalURL    *string         `json:"externalUrl" validate:"omitempty,http_url"`
	Notes          *string         `json:"notes"`
	// TaxDocKind is only meaningful when the document's Origin is TAX — see domain.go.
	TaxDocKind *TaxDocKind `json:"taxDocKind" validate:"omitempty,oneof=DAS DAS_DECLARATION NFSE FEDERAL_TAXES MONTHLY_CLOSING OTHER"`
}

// ClassifyDocumentRequest confirms the destination suggested by the Inbox without replacing any
// other document metadata.
type ClassifyDocumentRequest struct {
	Origin Origin `json:"origin" validate:"required,oneof=INVOICE PAYSTUB LAB_RESULT PERSONAL ASSET PET INSURANCE WARRANTY TAX CONTRACT RESUME OTHER"`
}

// ReconcileDocumentRequest is the JSON body for POST /api/documents/{id}/reconcile — the user (or
// the assistant, via its Inbox tool) answering the "qual o valor correto da fatura/holerite?"
// question raised when auto-extraction could not make the itemized lines sum to the printed
// total. StatementTotal is the authoritative number printed on the statement. ClosingMonth is
// optional — supply it only to correct a wrongly-read competência (any day in the month; it's
// normalized to day 1). After Reconcile the document's lines are moved to that competência and a
// single labelled adjustment line covers whatever the extraction still missed.
type ReconcileDocumentRequest struct {
	StatementTotal string          `json:"statementTotal" validate:"required,numeric"`
	ClosingMonth   *civildate.Date `json:"closingMonth"`
}

// ConfirmExtractionRequest is the JSON body for POST /api/documents/{id}/confirm-extraction — the
// Inbox review dialog handing back the same PendingExtraction shape the document was staged with,
// possibly edited. Exactly one of Fatura/Holerite/Exame must be set, matching Kind and the
// document's Origin (INVOICE/PAYSTUB/LAB_RESULT respectively) — checked in Service.
// ConfirmExtraction, not via a `validate` tag, since which pointer is required depends on Kind's
// runtime value.
type ConfirmExtractionRequest struct {
	PendingExtraction
}
