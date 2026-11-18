package documents

// This file defines the typed shape of what the AI extraction pipeline hands back for each
// origin that gets itemized extraction, and what a client sends back to
// POST /api/documents/{id}/confirm-extraction to accept (optionally edited) it. Same field names
// in Go and TypeScript (json tag = camelCase) on purpose — no translation layer at the frontend
// boundary. FaturaExtraida/HoleriteExtraido formalize structs that already existed inline in
// extract.go (invoiceExtraction/paystubExtraction); ExameExtraido is new — extractLabResult used
// to build an ad-hoc map[string]string instead of a named schema.

// FaturaExtraidaItem is one line the model pulled from a statement — a purchase, a fee, or a
// refund (Amount is signed: negative for estornos).
type FaturaExtraidaItem struct {
	Amount      string `json:"amount"`
	Date        string `json:"date"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Recurring   bool   `json:"recurring"`
	// CategorySource records how Category was chosen (AI/HISTORY) so confirmation can persist the
	// provenance. It round-trips through the review dialog untouched — the frontend spreads the
	// whole item — and defaults to AI on the server when absent.
	CategorySource string `json:"categorySource,omitempty"`
}

// FaturaExtraida is the itemized result for an origin=INVOICE document.
type FaturaExtraida struct {
	ClosingDate  string               `json:"closingDate"`
	DueDate      string               `json:"dueDate"`
	InvoiceTotal string               `json:"invoiceTotal"`
	Items        []FaturaExtraidaItem `json:"items"`
}

// HoleriteExtraidoLinha is one provento/desconto row. Kind is the model's own label
// ("PROVENTO"/"DESCONTO"), kept only for display — the sign on Amount is authoritative.
type HoleriteExtraidoLinha struct {
	Amount      string `json:"amount"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
}

// HoleriteExtraido is the itemized result for an origin=PAYSTUB document.
type HoleriteExtraido struct {
	ReferenceMonth string                  `json:"referenceMonth"`
	PayDate        string                  `json:"payDate"`
	TotalProventos string                  `json:"totalProventos"`
	TotalDescontos string                  `json:"totalDescontos"`
	NetPay         string                  `json:"netPay"`
	Lines          []HoleriteExtraidoLinha `json:"lines"`
}

// ExameExtraidoMarcador is one lab test result the model pulled from a LAB_RESULT document — e.g.
// "Colesterol Total" or "Glicemia de Jejum" — one line per marker, same idiom as
// FaturaExtraidaItem/HoleriteExtraidoLinha. Valor/ReferenciaMin/ReferenciaMax are kept as strings
// like every other money-shaped field in this codebase (numeric(10,2)-as-string, no float
// rounding). ReferenciaMin/Max are catalog-level facts (the marker's normal range), not
// per-result — they're only used to seed a new health_marker_catalog entry the first time this
// marker name is seen; an existing catalog entry's reference range is never overwritten.
type ExameExtraidoMarcador struct {
	Nome          string `json:"nome"`
	Valor         string `json:"valor"`
	Unidade       string `json:"unidade,omitempty"`
	ReferenciaMin string `json:"referenciaMin,omitempty"`
	ReferenciaMax string `json:"referenciaMax,omitempty"`
}

// ExameExtraido is the result for an origin=LAB_RESULT document. Assessment mirrors
// health.AssessmentStatus's closed set (NORMAL/ATTENTION/ALTERED) but is kept as a plain string
// here — this package doesn't import internal/health just to name the enum, and the confirm
// handler validates it the same way extractLabResult always has (parseAssessmentStatus). Markers
// is the itemized reading list (added 2026-09-17 — the old Java app's registros_saude had this
// per-marker granularity via legacy.marcadores_exame_catalogo, migrated once by scripts/etl into
// health_marker_catalog/health_records, but the Go rewrite's AI extraction never reproduced it for
// newly-uploaded exams until now); it may be empty for a document with no itemizable panel (a
// narrative-only report, a single organ assessment).
type ExameExtraido struct {
	Date       string                  `json:"date"`
	Assessment string                  `json:"assessment"`
	Organ      string                  `json:"organ,omitempty"`
	Notes      string                  `json:"notes"`
	Markers    []ExameExtraidoMarcador `json:"markers,omitempty"`
}

// PendingExtraction is the envelope stored in documents.pending_extraction (migration 000021)
// and exchanged with the client while a document sits at ExtractionAwaitingReview. Kind says
// which of the three sibling pointers is populated — more robust than letting the client infer
// the shape from the document's current Origin, which could be reclassified after extraction ran.
type PendingExtraction struct {
	Kind     string            `json:"kind"` // "FATURA" | "HOLERITE" | "EXAME"
	Fatura   *FaturaExtraida   `json:"fatura,omitempty"`
	Holerite *HoleriteExtraido `json:"holerite,omitempty"`
	Exame    *ExameExtraido    `json:"exame,omitempty"`
}

const (
	PendingExtractionKindFatura   = "FATURA"
	PendingExtractionKindHolerite = "HOLERITE"
	PendingExtractionKindExame    = "EXAME"
)
