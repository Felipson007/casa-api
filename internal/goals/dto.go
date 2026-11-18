package goals

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// GoalDTO is the JSON shape returned to clients. accumulated / gap / progressPct / monthsToTarget
// / onTrackForDate are derived (see Service.compute) — the client never sends them.
type GoalDTO struct {
	ID                  uuid.UUID       `json:"id"`
	Name                string          `json:"name"`
	Kind                Kind            `json:"kind"`
	TargetAmount        string          `json:"targetAmount"`
	TargetDate          *civildate.Date `json:"targetDate,omitempty"`
	CurrentAmount       string          `json:"currentAmount"`
	MonthlyContribution *string         `json:"monthlyContribution,omitempty"`
	FundingBalanceIDs   []uuid.UUID     `json:"fundingBalanceIds"`
	Notes               *string         `json:"notes,omitempty"`
	Status              Status          `json:"status"`
	CreatedAt           time.Time       `json:"createdAt"`
	UpdatedAt           time.Time       `json:"updatedAt"`

	// Derived.
	FundedFromBalances string `json:"fundedFromBalances"`
	Accumulated        string `json:"accumulated"`
	Gap                string `json:"gap"`
	ProgressPct        string `json:"progressPct"`
	MonthsToTarget     *int   `json:"monthsToTarget,omitempty"`
	OnTrackForDate     *bool  `json:"onTrackForDate,omitempty"`
}

// CreateGoalRequest is the body for POST /api/goals.
type CreateGoalRequest struct {
	Name                string          `json:"name" validate:"required,max=120"`
	Kind                Kind            `json:"kind" validate:"required,oneof=PURCHASE RESERVE DEBT_PAYOFF OTHER"`
	TargetAmount        string          `json:"targetAmount" validate:"required,numeric"`
	TargetDate          *civildate.Date `json:"targetDate"`
	CurrentAmount       *string         `json:"currentAmount" validate:"omitempty,numeric"`
	MonthlyContribution *string         `json:"monthlyContribution" validate:"omitempty,numeric"`
	FundingBalanceIDs   []uuid.UUID     `json:"fundingBalanceIds"`
	Notes               *string         `json:"notes"`
	Status              *Status         `json:"status" validate:"omitempty,oneof=ACTIVE ACHIEVED ARCHIVED"`
}

// UpdateGoalRequest is the body for PUT /api/goals/{id} — full replace, same convention as
// finance.UpdateBalanceRequest.
type UpdateGoalRequest struct {
	Name                string          `json:"name" validate:"required,max=120"`
	Kind                Kind            `json:"kind" validate:"required,oneof=PURCHASE RESERVE DEBT_PAYOFF OTHER"`
	TargetAmount        string          `json:"targetAmount" validate:"required,numeric"`
	TargetDate          *civildate.Date `json:"targetDate"`
	CurrentAmount       *string         `json:"currentAmount" validate:"omitempty,numeric"`
	MonthlyContribution *string         `json:"monthlyContribution" validate:"omitempty,numeric"`
	FundingBalanceIDs   []uuid.UUID     `json:"fundingBalanceIds"`
	Notes               *string         `json:"notes"`
	Status              Status          `json:"status" validate:"required,oneof=ACTIVE ACHIEVED ARCHIVED"`
}
