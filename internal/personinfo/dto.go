package personinfo

import (
	"time"

	"github.com/google/uuid"
)

// InformationDTO is the JSON shape returned to clients.
type InformationDTO struct {
	ID           uuid.UUID  `json:"id"`
	PersonID     uuid.UUID  `json:"personId"`
	Category     Category   `json:"category"`
	Key          string     `json:"key"`
	Value        string     `json:"value"`
	DocumentID   *uuid.UUID `json:"documentId,omitempty"`
	Origin       Origin     `json:"origin"`
	DocumentType *string    `json:"documentType,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
}

// CreateInformationRequest is the JSON body accepted by POST /api/person-information. Origin is
// never accepted here — derived server-side from whether DocumentID resolves (mirrors Java's
// criar, which computes origem the same way, never from client input).
type CreateInformationRequest struct {
	PersonID     uuid.UUID  `json:"personId" validate:"required"`
	Category     Category   `json:"category" validate:"required,oneof=PERSONAL ACADEMIC PROFESSIONAL OTHER"`
	Key          string     `json:"key" validate:"required"`
	Value        string     `json:"value" validate:"required"`
	DocumentType *string    `json:"documentType"`
	DocumentID   *uuid.UUID `json:"documentId"`
}

// UpdateInformationRequest is the JSON body accepted by PUT /api/person-information/{id} —
// full-replacement of Category/Key/Value/DocumentType only. DocumentID/Origin/PersonID are not
// editable here — mirrors Java's atualizar exactly (documentoId/origem stay historical facts).
type UpdateInformationRequest struct {
	Category     Category `json:"category" validate:"required,oneof=PERSONAL ACADEMIC PROFESSIONAL OTHER"`
	Key          string   `json:"key" validate:"required"`
	Value        string   `json:"value" validate:"required"`
	DocumentType *string  `json:"documentType"`
}

// CreateInformationBatchItem is one fact within a batch — mirrors Java's inner Item record
// (categoria/chave/valor only; documentType/documentId are batch-level, shared across every item).
type CreateInformationBatchItem struct {
	Category Category `json:"category" validate:"required,oneof=PERSONAL ACADEMIC PROFESSIONAL OTHER"`
	Key      string   `json:"key" validate:"required"`
	Value    string   `json:"value" validate:"required"`
}

// CreateInformationBatchRequest is the JSON body accepted by POST /api/person-information/batch —
// the post-upload extraction review screen's "confirm all these facts at once" flow (mirrors
// criarEmLote): one document lookup for every item, instead of N calls to create.
type CreateInformationBatchRequest struct {
	PersonID     uuid.UUID                    `json:"personId" validate:"required"`
	DocumentType *string                      `json:"documentType"`
	DocumentID   *uuid.UUID                   `json:"documentId"`
	Items        []CreateInformationBatchItem `json:"items" validate:"required,min=1,dive"`
}

// EqualizeInformationRequest is the JSON body accepted by POST /api/person-information/equalize —
// applies one chosen-correct Value to every listed row (mirrors Java's equalizar, called from the
// "inconsistência" resolution flow: several facts that should agree but don't). Only Value
// changes; Category/Key/DocumentType on each row are left untouched.
type EqualizeInformationRequest struct {
	InformationIDs []uuid.UUID `json:"informationIds" validate:"required,min=1"`
	Value          string      `json:"value" validate:"required"`
}

// HistoryEntryDTO is the JSON shape for GET /api/person-information/{id}/history — most recent
// first, mirrors PessoaInformacaoHistoricoDto (no informationId field: implicit from the URL).
type HistoryEntryDTO struct {
	ID           uuid.UUID `json:"id"`
	Category     Category  `json:"category"`
	Key          string    `json:"key"`
	Value        string    `json:"value"`
	DocumentType *string   `json:"documentType,omitempty"`
	ChangedAt    time.Time `json:"changedAt"`
}
