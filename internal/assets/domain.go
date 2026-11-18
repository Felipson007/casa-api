// Package assets is the personal-property module (bens in the old app) — owned by Claude per
// decisions/DECISIONS.md D-005. Mirrors internal/clients' shape.
package assets

import (
	"time"

	"github.com/google/uuid"
)

// Status mirrors the assets.status CHECK constraint.
type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusSold     Status = "SOLD"
	StatusInactive Status = "INACTIVE"
)

// AssetAttribute is one entry of Asset.Attributes — a component/spec of the asset (e.g. one part
// of a gaming PC), not its own table because the list is always small — mirrors the old Java
// AtributoBem record's comment ("JSON, not its own table"). EstimatedValue/ValueExtractedAt are
// only ever filled by a scheduled price crawler, never set directly by the user.
type AssetAttribute struct {
	Name             string     `json:"name"`
	Type             string     `json:"type"`
	Value            *string    `json:"value,omitempty"`
	EstimatedValue   *string    `json:"estimatedValue,omitempty"`
	ValueExtractedAt *time.Time `json:"valueExtractedAt,omitempty"`
}

// Asset mirrors the assets table (internal/migrations/000001_init_schema.up.sql). PersonID and
// CategoryID are nullable FKs with ON DELETE SET NULL. Money fields are Go string, not float64 —
// same reasoning as loans.Loan/expenses.Expense. Attributes is the jsonb column (defaults to an
// empty JSON array).
type Asset struct {
	ID                    uuid.UUID
	PersonID              *uuid.UUID
	Name                  string
	CategoryID            *uuid.UUID
	Identification        *string
	AcquisitionValue      *string
	AcquisitionDate       *time.Time
	CurrentEstimatedValue *string
	Status                Status
	HasDebt               bool
	DebtAmount            *string
	Notes                 *string
	Attributes            []AssetAttribute
	CreatedAt             time.Time
}
