// Package finance is the financial-balances module (patrimonio/saldos_financeiros in the old app)
// — owned by Claude per decisions/DECISIONS.md D-005. A Balance is a financial balance that's
// neither salary nor a physical Asset (FGTS, private pension, savings, checking, ...) — counts
// toward net worth, updated manually (no rate-of-return/contribution tracking; the old Java
// javadoc on SaldoFinanceiro is explicit that's a deliberately out-of-scope "Investments" module).
package finance

import (
	"time"

	"github.com/google/uuid"
)

// BalanceType mirrors the financial_balances.balance_type CHECK constraint.
type BalanceType string

const (
	BalanceTypeFGTS           BalanceType = "FGTS"
	BalanceTypePrivatePension BalanceType = "PRIVATE_PENSION"
	BalanceTypeSavings        BalanceType = "SAVINGS"
	BalanceTypeChecking       BalanceType = "CHECKING"
	BalanceTypeOther          BalanceType = "OTHER"
)

// Balance mirrors the financial_balances table (internal/migrations/000001_init_schema.up.sql).
// PersonID is a nullable FK with ON DELETE SET NULL — nil means shared/household, same convention
// as assets.PersonID. CurrentValue is Go string, not float64 — same reasoning as
// loans.Loan/expenses.Expense.
type Balance struct {
	ID           uuid.UUID
	PersonID     *uuid.UUID
	Name         string
	Type         BalanceType
	CurrentValue string
	UpdatedOn    time.Time
	Notes        *string
	CreatedAt    time.Time
	// BankAccountID is set when the balance is owned by the Open Finance sync (it mirrors a bank
	// account): its value is not editable by hand and the row can't be deleted — the next sync
	// would only recreate it.
	BankAccountID *uuid.UUID
}

// BalanceHistoryEntry mirrors financial_balance_history — a snapshot of a Balance's previous
// CurrentValue/UpdatedOn, written just before an update actually changes either (see
// Service.Update). Append-only: nothing in this module ever updates or deletes a history row
// directly (rows disappear only via ON DELETE CASCADE when their Balance is deleted).
type BalanceHistoryEntry struct {
	ID         uuid.UUID
	BalanceID  uuid.UUID
	Balance    string
	RecordedOn time.Time
	CreatedAt  time.Time
}
