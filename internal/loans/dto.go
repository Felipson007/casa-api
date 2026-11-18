package loans

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// LoanDTO is the JSON shape returned to clients. Money fields are strings (e.g. "1500.00") to
// avoid float rounding — same reasoning as Loan.PrincipalAmount in domain.go. ContractDate/
// PaidOffDate are civildate.Date, not time.Time: a calendar date has no time-of-day, and
// marshaling one as a full RFC3339 instant (e.g. "2026-08-05T00:00:00Z") reads as the day before
// in any negative-UTC-offset timezone once a client formats it in local time (see civildate's doc
// comment) — CreatedAt is a real instant, so it keeps time.Time.
type LoanDTO struct {
	ID                  uuid.UUID       `json:"id"`
	ClientID            uuid.UUID       `json:"clientId"`
	PrincipalAmount     string          `json:"principalAmount"`
	MonthlyInterestRate string          `json:"monthlyInterestRate"`
	PaymentType         PaymentType     `json:"paymentType"`
	InstallmentCount    int             `json:"installmentCount"`
	ContractDate        civildate.Date  `json:"contractDate"`
	Status              Status          `json:"status"`
	OutstandingBalance  string          `json:"outstandingBalance"`
	DueDay              int             `json:"dueDay"`
	PaidOffDate         *civildate.Date `json:"paidOffDate,omitempty"`
	LegacySource        *string         `json:"legacySource,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
}

// CreateLoanRequest is the JSON body accepted by POST /api/loans. OutstandingBalance is not
// accepted here — a new loan's outstanding balance always starts equal to PrincipalAmount.
// InstallmentCount is only meaningful (and required) for PaymentTypeInstallment — a
// RECURRING_INTEREST loan's installment count isn't known up front (see schedule.go); Service.Create
// enforces this, since it depends on PaymentType and can't be expressed as a single struct tag.
// SkipFirstMonth mirrors CreateEmprestimoRequest.pularPrimeiroMes: false (the default) means the
// first installment/charge falls 1 month after ContractDate; true skips to 2 months.
type CreateLoanRequest struct {
	ClientID            uuid.UUID      `json:"clientId" validate:"required"`
	PrincipalAmount     string         `json:"principalAmount" validate:"required,numeric"`
	MonthlyInterestRate string         `json:"monthlyInterestRate" validate:"required,numeric"`
	PaymentType         PaymentType    `json:"paymentType" validate:"required,oneof=INSTALLMENT RECURRING_INTEREST"`
	InstallmentCount    int            `json:"installmentCount" validate:"omitempty,min=1"`
	ContractDate        civildate.Date `json:"contractDate" validate:"required"`
	DueDay              int            `json:"dueDay" validate:"required,min=1,max=31"`
	SkipFirstMonth      bool           `json:"skipFirstMonth"`
	LegacySource        *string        `json:"legacySource"`
}

// ChangeDueDayRequest is the JSON body accepted by PATCH /api/loans/{id}/due-day.
type ChangeDueDayRequest struct {
	DueDay int `json:"dueDay" validate:"required,min=1,max=31"`
}

// PostponeInstallmentRequest is the JSON body accepted by
// PATCH /api/loans/{id}/installments/{installmentId}/postpone.
type PostponeInstallmentRequest struct {
	NewDueDate civildate.Date `json:"newDueDate" validate:"required"`
}

// ChangeInterestRateRequest is the JSON body accepted by PATCH /api/loans/{id}/interest-rate.
// The rate is a monthly percentage (e.g. "7.5" = 7.5% a month); "0" is allowed (an interest-free
// arrangement). Only RECURRING_INTEREST loans are eligible — see Service.ChangeInterestRate.
type ChangeInterestRateRequest struct {
	MonthlyInterestRate string `json:"monthlyInterestRate" validate:"required,numeric"`
}

// InstallmentDTO is the JSON shape for GET /api/loans/{id}/installments. DueDate is civildate.Date
// — see LoanDTO's doc comment on why (this is the exact field reported showing "one day less").
type InstallmentDTO struct {
	ID                 uuid.UUID         `json:"id"`
	LoanID             uuid.UUID         `json:"loanId"`
	InstallmentNumber  int               `json:"installmentNumber"`
	DueDate            civildate.Date    `json:"dueDate"`
	InstallmentAmount  string            `json:"installmentAmount"`
	OutstandingBalance string            `json:"outstandingBalance"`
	PrincipalAmount    string            `json:"principalAmount"`
	InterestAmount     string            `json:"interestAmount"`
	Status             InstallmentStatus `json:"status"`
	CreatedAt          time.Time         `json:"createdAt"`
}

// MarkInstallmentPaidRequest is the JSON body accepted by
// POST /api/loans/{loanId}/installments/{installmentId}/pay. AmountReceived/PaymentDate default to
// the installment's full InstallmentAmount and today, respectively, when omitted — mirrors
// MarkParcelaPaidRequest exactly.
type MarkInstallmentPaidRequest struct {
	AmountReceived *string         `json:"amountReceived" validate:"omitempty,numeric"`
	PaymentDate    *civildate.Date `json:"paymentDate"`
}

// ApplyAmortizationRequest is the JSON body accepted by POST /api/loans/{id}/amortize.
type ApplyAmortizationRequest struct {
	Amount      string         `json:"amount" validate:"required,numeric"`
	PaymentDate civildate.Date `json:"paymentDate" validate:"required"`
	Description *string        `json:"description"`
}

// TransactionDTO is the JSON shape returned to clients. PaymentDate is civildate.Date — see
// LoanDTO's doc comment on why.
type TransactionDTO struct {
	ID            uuid.UUID       `json:"id"`
	ClientID      uuid.UUID       `json:"clientId"`
	LoanID        *uuid.UUID      `json:"loanId,omitempty"`
	InstallmentID *uuid.UUID      `json:"installmentId,omitempty"`
	Type          TransactionType `json:"type"`
	Amount        string          `json:"amount"`
	PaymentDate   civildate.Date  `json:"paymentDate"`
	Description   *string         `json:"description,omitempty"`
	LegacySource  *string         `json:"legacySource,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
}

// CreateTransactionRequest is the JSON body accepted by POST /api/loans/{id}/transactions. Type is
// restricted to ONE_OFF_PAYMENT/FORGIVENESS — DISBURSEMENT/INSTALLMENT_PAYMENT/AMORTIZATION are
// only ever recorded as the automatic side effect of Create/MarkInstallmentPaid/ApplyAmortization,
// never authored directly (see TransactionType's doc comments in domain.go).
type CreateTransactionRequest struct {
	Type        TransactionType `json:"type" validate:"required,oneof=ONE_OFF_PAYMENT FORGIVENESS"`
	Amount      string          `json:"amount" validate:"required,numeric"`
	PaymentDate civildate.Date  `json:"paymentDate" validate:"required"`
	Description *string         `json:"description"`
}
