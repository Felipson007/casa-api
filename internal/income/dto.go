package income

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// EntryDTO is the JSON shape returned to clients. IncomeDate/OriginalDate are civildate.Date, not
// time.Time — see its doc comment.
type EntryDTO struct {
	ID           uuid.UUID       `json:"id"`
	DocumentID   *uuid.UUID      `json:"documentId,omitempty"`
	CategoryID   *uuid.UUID      `json:"categoryId,omitempty"`
	PersonID     *uuid.UUID      `json:"personId,omitempty"`
	Amount       string          `json:"amount"`
	IncomeDate   civildate.Date  `json:"incomeDate"`
	OriginalDate *civildate.Date `json:"originalDate,omitempty"`
	Description  *string         `json:"description,omitempty"`
	Reviewed     bool            `json:"reviewed"`
	CreatedAt    time.Time       `json:"createdAt"`
	Source       string          `json:"source"`
	// CategorySource is how the category was chosen (NONE/AI/RULE/HISTORY/MANUAL) — see domain.go.
	CategorySource string `json:"categorySource"`

	// USDAmount/ExchangeRate are only present together — see Entry's doc comment.
	USDAmount    *string `json:"usdAmount,omitempty"`
	ExchangeRate *string `json:"exchangeRate,omitempty"`
}

// CreateEntryRequest is the JSON body accepted by POST /api/income. Reviewed always starts false
// at creation — mirrors the DB column default.
type CreateEntryRequest struct {
	DocumentID   *uuid.UUID      `json:"documentId"`
	CategoryID   *uuid.UUID      `json:"categoryId"`
	PersonID     *uuid.UUID      `json:"personId"`
	Amount       string          `json:"amount" validate:"required,numeric"`
	IncomeDate   civildate.Date  `json:"incomeDate" validate:"required"`
	OriginalDate *civildate.Date `json:"originalDate"`
	Description  *string         `json:"description"`
	// USDAmount/ExchangeRate must be given together (validated in Service.Create) — see Entry's doc
	// comment on domain.go.
	USDAmount    *string `json:"usdAmount" validate:"omitempty,numeric"`
	ExchangeRate *string `json:"exchangeRate" validate:"omitempty,numeric"`
	// CategorySource is not client-settable (json:"-") — internal plumbing for callers that know
	// the provenance; the HTTP path derives it in Service.Create.
	CategorySource string `json:"-"`
}

// UpdateEntryRequest is the JSON body accepted by PUT /api/income/{id}. Reviewed is settable
// here, same reasoning as expenses.UpdateExpenseRequest.
type UpdateEntryRequest struct {
	DocumentID   *uuid.UUID      `json:"documentId"`
	CategoryID   *uuid.UUID      `json:"categoryId"`
	PersonID     *uuid.UUID      `json:"personId"`
	Amount       string          `json:"amount" validate:"required,numeric"`
	IncomeDate   civildate.Date  `json:"incomeDate" validate:"required"`
	OriginalDate *civildate.Date `json:"originalDate"`
	Description  *string         `json:"description"`
	Reviewed     bool            `json:"reviewed"`
	USDAmount    *string         `json:"usdAmount" validate:"omitempty,numeric"`
	ExchangeRate *string         `json:"exchangeRate" validate:"omitempty,numeric"`
}
