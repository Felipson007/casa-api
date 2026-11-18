// Package goals is the financial-planning module: a Goal is a target the household is saving
// toward (a house down payment, an emergency reserve, paying off a debt). It records the target
// amount, an optional deadline, a manual "already put aside" baseline, an optional monthly
// contribution, and which financial_balances (FGTS, a savings bucket, ...) count toward it.
// Deliberately out of scope: rate-of-return projections, automatic contributions.
package goals

import (
	"time"

	"github.com/google/uuid"
)

// Kind is the financial_goals.kind CHECK.
type Kind string

const (
	KindPurchase   Kind = "PURCHASE"
	KindReserve    Kind = "RESERVE"
	KindDebtPayoff Kind = "DEBT_PAYOFF"
	KindOther      Kind = "OTHER"
)

// Status is the financial_goals.status CHECK.
type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusAchieved Status = "ACHIEVED"
	StatusArchived Status = "ARCHIVED"
)

// Goal mirrors the financial_goals row plus FundedFromBalances, which is not stored — the
// repository resolves it at read time by summing the current value of every financial_balance
// listed in FundingBalanceIDs.
type Goal struct {
	ID                  uuid.UUID
	Name                string
	Kind                Kind
	TargetAmount        string
	TargetDate          *time.Time
	CurrentAmount       string
	MonthlyContribution *string
	FundingBalanceIDs   []uuid.UUID
	Notes               *string
	Status              Status
	CreatedAt           time.Time
	UpdatedAt           time.Time

	// FundedFromBalances is Σ(current_value) of FundingBalanceIDs, computed by the repository.
	FundedFromBalances string
}
