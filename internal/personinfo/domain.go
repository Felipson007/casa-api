// Package personinfo ports pessoa_informacoes (br.com.atlas.pessoainformacoes) — structured
// personal/academic/professional facts about a person (RG, CPF, diploma, job title, ...),
// extracted from a scanned document or typed manually, with an edit history. Read from the real
// Java source (PessoaInformacaoService/Controller), not guessed.
//
// The _historico table folds into the generic audit_logs table (architecture-plan.md section 10,
// item 6 — a pure edit-trail nobody charts, unlike financial_balance_history) — this package's own
// postgres.Repository writes directly to audit_logs inside the same transaction as an update,
// mirroring internal/finance's Update(ctx, b, snapshot) idiom exactly (never going through
// internal/audit's own HTTP-facing Service, which would need its own separate transaction and
// break the atomicity Java's single @Transactional method has).
package personinfo

import (
	"time"

	"github.com/google/uuid"
)

// Category mirrors CategoriaInformacaoPessoa, translated to the person_information.category CHECK
// constraint's English values. MEDICAL exists in the CHECK (added ahead of this port, no Java
// counterpart) but is never produced here — the Java enum only ever had 4 values.
type Category string

const (
	CategoryPersonal     Category = "PERSONAL"
	CategoryAcademic     Category = "ACADEMIC"
	CategoryProfessional Category = "PROFESSIONAL"
	CategoryOther        Category = "OTHER"
)

// Origin mirrors OrigemInformacaoPessoa. EXTRAIDO maps to IMPORT, NOT OCR — scripts/etl/
// transform.sql already made this exact call (CASE origem WHEN 'EXTRAIDO' THEN 'IMPORT') before
// this module existed, and real production data (31 rows, all EXTRAIDO in Java) is already loaded
// as IMPORT — mapping to OCR here would silently fail to round-trip every real existing row.
// OCR is reserved for a future real OCR pipeline; nothing produces it yet, legacy included.
type Origin string

const (
	OriginManual Origin = "MANUAL"
	OriginImport Origin = "IMPORT"
	OriginOCR    Origin = "OCR" // reserved for future use — never produced today
)

// Information mirrors the person_information table. Origin is never client-supplied — it's
// derived from whether DocumentID resolved to a real document at creation time (see
// Service.Create/CreateBatch), exactly like Java's criar/criarEmLote.
type Information struct {
	ID           uuid.UUID
	PersonID     uuid.UUID
	Category     Category
	Key          string
	Value        string
	DocumentID   *uuid.UUID
	Origin       Origin
	DocumentType *string
	CreatedAt    time.Time
}

// HistoryEntry is one audit_logs row's old_data, reshaped into the flat fields
// PessoaInformacaoHistoricoDto exposed — a snapshot of what Category/Key/Value/DocumentType were
// right before they got overwritten.
type HistoryEntry struct {
	ID           uuid.UUID
	Category     Category
	Key          string
	Value        string
	DocumentType *string
	ChangedAt    time.Time
}
