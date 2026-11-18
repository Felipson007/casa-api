package expenses

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// ExpenseDTO is the JSON shape returned to clients. ExpenseDate/OriginalDate are civildate.Date,
// not time.Time — see its doc comment.
type ExpenseDTO struct {
	ID           uuid.UUID       `json:"id"`
	DocumentID   *uuid.UUID      `json:"documentId,omitempty"`
	CategoryID   *uuid.UUID      `json:"categoryId,omitempty"`
	PersonID     *uuid.UUID      `json:"personId,omitempty"`
	Amount       string          `json:"amount"`
	ExpenseDate  civildate.Date  `json:"expenseDate"`
	Description  *string         `json:"description,omitempty"`
	Reviewed     bool            `json:"reviewed"`
	OriginalDate *civildate.Date `json:"originalDate,omitempty"`
	Recurring    bool            `json:"recurring"`
	CreatedAt    time.Time       `json:"createdAt"`
	Source       string          `json:"source"`
	// CategorySource is how the category was chosen (NONE/AI/RULE/HISTORY/MANUAL) — see domain.go.
	CategorySource string `json:"categorySource"`
}

// CreateExpenseRequest is the JSON body accepted by POST /api/expenses. Reviewed always starts
// false at creation — mirrors the DB column default.
type CreateExpenseRequest struct {
	DocumentID   *uuid.UUID      `json:"documentId"`
	CategoryID   *uuid.UUID      `json:"categoryId"`
	PersonID     *uuid.UUID      `json:"personId"`
	Amount       string          `json:"amount" validate:"required,numeric"`
	ExpenseDate  civildate.Date  `json:"expenseDate" validate:"required"`
	Description  *string         `json:"description"`
	OriginalDate *civildate.Date `json:"originalDate"`
	Recurring    bool            `json:"recurring"`
	// CategorySource is not client-settable (json:"-") — it is plumbing for the internal callers
	// that know the provenance (documents.ReplaceForDocument carries AI/HISTORY from the review
	// draft). The HTTP path derives it in Service.Create.
	CategorySource string `json:"-"`
}

// UpdateExpenseRequest is the JSON body accepted by PUT /api/expenses/{id}. Unlike create,
// Reviewed is settable here — marking an expense reviewed is the expected post-creation edit.
type UpdateExpenseRequest struct {
	DocumentID   *uuid.UUID      `json:"documentId"`
	CategoryID   *uuid.UUID      `json:"categoryId"`
	PersonID     *uuid.UUID      `json:"personId"`
	Amount       string          `json:"amount" validate:"required,numeric"`
	ExpenseDate  civildate.Date  `json:"expenseDate" validate:"required"`
	Description  *string         `json:"description"`
	Reviewed     bool            `json:"reviewed"`
	OriginalDate *civildate.Date `json:"originalDate"`
	Recurring    bool            `json:"recurring"`
}
