package assets

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// AssetDTO is the JSON shape returned to clients. AcquisitionDate is civildate.Date, not
// time.Time — see its doc comment.
type AssetDTO struct {
	ID                    uuid.UUID        `json:"id"`
	PersonID              *uuid.UUID       `json:"personId,omitempty"`
	Name                  string           `json:"name"`
	CategoryID            *uuid.UUID       `json:"categoryId,omitempty"`
	Identification        *string          `json:"identification,omitempty"`
	AcquisitionValue      *string          `json:"acquisitionValue,omitempty"`
	AcquisitionDate       *civildate.Date  `json:"acquisitionDate,omitempty"`
	CurrentEstimatedValue *string          `json:"currentEstimatedValue,omitempty"`
	Status                Status           `json:"status"`
	HasDebt               bool             `json:"hasDebt"`
	DebtAmount            *string          `json:"debtAmount,omitempty"`
	Notes                 *string          `json:"notes,omitempty"`
	Attributes            []AssetAttribute `json:"attributes"`
	CreatedAt             time.Time        `json:"createdAt"`
}

// CreateAssetRequest is the JSON body accepted by POST /api/assets. Status always starts ACTIVE —
// mirrors the DB column default.
type CreateAssetRequest struct {
	PersonID              *uuid.UUID       `json:"personId"`
	Name                  string           `json:"name" validate:"required"`
	CategoryID            *uuid.UUID       `json:"categoryId"`
	Identification        *string          `json:"identification"`
	AcquisitionValue      *string          `json:"acquisitionValue" validate:"omitempty,numeric"`
	AcquisitionDate       *civildate.Date  `json:"acquisitionDate"`
	CurrentEstimatedValue *string          `json:"currentEstimatedValue" validate:"omitempty,numeric"`
	HasDebt               bool             `json:"hasDebt"`
	DebtAmount            *string          `json:"debtAmount" validate:"omitempty,numeric"`
	Notes                 *string          `json:"notes"`
	Attributes            []AssetAttribute `json:"attributes"`
}

// UpdateAssetRequest is the JSON body accepted by PUT /api/assets/{id}. Status is settable here —
// e.g. transitioning ACTIVE -> SOLD/INACTIVE.
type UpdateAssetRequest struct {
	PersonID              *uuid.UUID       `json:"personId"`
	Name                  string           `json:"name" validate:"required"`
	CategoryID            *uuid.UUID       `json:"categoryId"`
	Identification        *string          `json:"identification"`
	AcquisitionValue      *string          `json:"acquisitionValue" validate:"omitempty,numeric"`
	AcquisitionDate       *civildate.Date  `json:"acquisitionDate"`
	CurrentEstimatedValue *string          `json:"currentEstimatedValue" validate:"omitempty,numeric"`
	Status                Status           `json:"status" validate:"required,oneof=ACTIVE SOLD INACTIVE"`
	HasDebt               bool             `json:"hasDebt"`
	DebtAmount            *string          `json:"debtAmount" validate:"omitempty,numeric"`
	Notes                 *string          `json:"notes"`
	Attributes            []AssetAttribute `json:"attributes"`
}
