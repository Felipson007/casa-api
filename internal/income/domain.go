// Package income is the personal-income-tracking module (receitas in the old app) — owned by
// Claude per decisions/DECISIONS.md D-005. Mirrors internal/expenses' shape (its closest sibling
// table — income_entries has the same nullable-FK/reviewed-flag shape as expenses, minus a
// "recurring" column).
package income

import (
	"time"

	"github.com/google/uuid"
)

// SourcePluggy marks an income entry synced from the bank: amount and date belong to the bank.
const SourcePluggy = "PLUGGY"

// CategorySource values (migration 000035) — same semantics as expenses.CategorySource*.
const (
	CategorySourceNone    = "NONE"
	CategorySourceAI      = "AI"
	CategorySourceRule    = "RULE"
	CategorySourceHistory = "HISTORY"
	CategorySourceManual  = "MANUAL"
)

// Entry mirrors the income_entries table (internal/migrations/000001_init_schema.up.sql). All
// three FKs (DocumentID, CategoryID, PersonID) are nullable with ON DELETE SET NULL.
type Entry struct {
	ID           uuid.UUID
	DocumentID   *uuid.UUID
	CategoryID   *uuid.UUID
	PersonID     *uuid.UUID
	Amount       string // numeric(15,2) — kept as string to avoid float rounding, same as expenses.Expense
	IncomeDate   time.Time
	OriginalDate *time.Time
	Description  *string
	Reviewed     bool
	CreatedAt    time.Time

	// USDAmount/ExchangeRate (migration 000019) are optional and only meaningful together — a
	// foreign employer paying in USD, converted to BRL at the rate used that month (e.g. via Wise).
	// Amount (BRL) remains the source of truth for every total/report; these are purely
	// informational, surfaced in MonthlySummaryDTO for the months they're set.
	USDAmount    *string
	ExchangeRate *string

	// Source is MANUAL, DOCUMENT or PLUGGY (synced from the bank) — see expenses.Expense.Source.
	Source string

	// CategorySource is how CategoryID was chosen — MANUAL is authoritative and never overwritten
	// by autocategorisation. See expenses.CategorySource*.
	CategorySource string
}
