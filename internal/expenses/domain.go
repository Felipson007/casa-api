// Package expenses is the personal-expense-tracking module (gastos in the old app) — owned by
// Claude per decisions/DECISIONS.md D-005. Mirrors internal/clients' shape.
package expenses

import (
	"time"

	"github.com/google/uuid"
)

// SourcePluggy marks an expense synced from the bank (Open Finance): its amount and dates belong to
// the bank, so edits may only change category, person, description-free flags and review state.
const SourcePluggy = "PLUGGY"

// CategorySource values (migration 000035) record how a row got its category, so later launches
// with the same or a similar description can inherit it and a human correction always wins.
const (
	CategorySourceNone    = "NONE"
	CategorySourceAI      = "AI"
	CategorySourceRule    = "RULE"
	CategorySourceHistory = "HISTORY"
	CategorySourceManual  = "MANUAL"
)

// Expense mirrors the expenses table (internal/migrations/000001_init_schema.up.sql). All three
// FKs (DocumentID, CategoryID, PersonID) are nullable with ON DELETE SET NULL — an expense
// survives its linked document/category/person being deleted, it just loses that link.
type Expense struct {
	ID           uuid.UUID
	DocumentID   *uuid.UUID
	CategoryID   *uuid.UUID
	PersonID     *uuid.UUID
	Amount       string // numeric(15,2) — kept as string to avoid float rounding, same as loans.Loan
	ExpenseDate  time.Time
	Description  *string
	Reviewed     bool
	OriginalDate *time.Time
	Recurring    bool
	CreatedAt    time.Time
	// Source is where the row came from: MANUAL, DOCUMENT (extracted from an uploaded invoice) or
	// PLUGGY (synced from the bank). Written by the sync, never by Create/Update.
	Source string
	// CategorySource is how CategoryID was chosen — see the CategorySource* constants. MANUAL is
	// authoritative: autocategorisation never overwrites it.
	CategorySource string
}
