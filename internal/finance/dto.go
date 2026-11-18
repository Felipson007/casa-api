package finance

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// BalanceDTO is the JSON shape returned to clients. UpdatedOn is civildate.Date, not time.Time —
// see its doc comment (a calendar date has no time-of-day/zone to misinterpret in local
// formatting, and the frontend's date input only ever sends/expects a bare "YYYY-MM-DD").
type BalanceDTO struct {
	ID           uuid.UUID      `json:"id"`
	PersonID     *uuid.UUID     `json:"personId,omitempty"`
	Name         string         `json:"name"`
	Type         BalanceType    `json:"type"`
	CurrentValue string         `json:"currentValue"`
	UpdatedOn    civildate.Date `json:"updatedOn"`
	Notes        *string        `json:"notes,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
	// Automatic is true for balances synced from a bank account (Open Finance).
	Automatic bool `json:"automatic"`
}

// CreateBalanceRequest is the JSON body accepted by POST /api/finance/balances.
type CreateBalanceRequest struct {
	PersonID     *uuid.UUID     `json:"personId"`
	Name         string         `json:"name" validate:"required"`
	Type         BalanceType    `json:"type" validate:"required,oneof=FGTS PRIVATE_PENSION SAVINGS CHECKING OTHER"`
	CurrentValue string         `json:"currentValue" validate:"required,numeric"`
	UpdatedOn    civildate.Date `json:"updatedOn" validate:"required"`
	Notes        *string        `json:"notes"`
}

// UpdateBalanceRequest is the JSON body accepted by PUT /api/finance/balances/{id}. If
// CurrentValue or UpdatedOn actually differ from the stored row, the previous value is snapshotted
// into BalanceHistoryEntry before being overwritten (see Service.Update) — mirrors the old Java
// SaldoFinanceiroService.atualizar exactly, including not snapshotting on a no-op edit (e.g. only
// Name/Notes changed).
type UpdateBalanceRequest struct {
	PersonID     *uuid.UUID     `json:"personId"`
	Name         string         `json:"name" validate:"required"`
	Type         BalanceType    `json:"type" validate:"required,oneof=FGTS PRIVATE_PENSION SAVINGS CHECKING OTHER"`
	CurrentValue string         `json:"currentValue" validate:"required,numeric"`
	UpdatedOn    civildate.Date `json:"updatedOn" validate:"required"`
	Notes        *string        `json:"notes"`
}

// BalanceHistoryEntryDTO is the JSON shape returned by GET /api/finance/balances/{id}/history.
type BalanceHistoryEntryDTO struct {
	ID         uuid.UUID      `json:"id"`
	Balance    string         `json:"balance"`
	RecordedOn civildate.Date `json:"recordedOn"`
	CreatedAt  time.Time      `json:"createdAt"`
}
