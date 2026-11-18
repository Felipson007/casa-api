// Package loans is the loan-book module (emprestimos in the old app) — owned by Claude per
// decisions/DECISIONS.md D-005.
//
// Part 2 (this pass) adds what D-011 deliberately deferred: schedule generation at Create
// (schedule.go — fixed up front for PaymentTypeInstallment, first-year batch for
// PaymentTypeRecurringInterest), installment payment recording (MarkInstallmentPaid), principal
// paydowns (ApplyAmortization), and the loan_transactions ledger (movimentacoes) — all read from
// the real Java EmprestimoService/ParcelaService/EmprestimoRecorrenteService/MovimentacaoService
// rather than guessed.
//
// Deliberately still out of scope (documented here rather than silently done): the daily rollover
// job that extends a RECURRING_INTEREST loan's schedule year over year (a scheduled background
// job, not a request-time calculation — see schedule.go's doc on
// GenerateRecurringChargesThroughYearEnd); full-replacement Update, due-day changes, and the soft
// Cancel status transition (EmprestimoService.atualizar/alterarDiaVencimento/cancelar); the
// balance-ranking dashboard aggregation; and PDF payment receipts (relatorios/PdfGenerationService
// — Hermes-owned platform concern, not this module's).
package loans

import (
	"time"

	"github.com/google/uuid"
)

// PaymentType mirrors the loans.payment_type CHECK constraint (fixed by D-011 to match the real
// legacy TipoPagamento enum 1:1 — PARCELADO/UNICO, not the earlier fictional FIXED/COMPOUND/BALLOON).
type PaymentType string

const (
	// PaymentTypeInstallment is PARCELADO: N fixed installments, each splitting principal (flat
	// principal/n, last installment absorbs the rounding remainder) and interest (flat
	// principal×rate%, not declining-balance).
	PaymentTypeInstallment PaymentType = "INSTALLMENT"
	// PaymentTypeRecurringInterest is UNICO: an interest-only recurring charge on the current
	// outstanding_balance; principal never reduces on payment, only via explicit amortization.
	PaymentTypeRecurringInterest PaymentType = "RECURRING_INTEREST"
)

// Status mirrors the loans.status CHECK constraint.
type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusOverdue  Status = "OVERDUE"
	StatusPaidOff  Status = "PAID_OFF"
	StatusCanceled Status = "CANCELLED"
)

// Loan mirrors the loans table (internal/migrations/000001_init_schema.up.sql).
type Loan struct {
	ID                  uuid.UUID
	ClientID            uuid.UUID
	PrincipalAmount     string // numeric(15,2) — kept as string to avoid float rounding; see mapper.go
	MonthlyInterestRate string // numeric(12,6)
	PaymentType         PaymentType
	InstallmentCount    int
	ContractDate        time.Time
	Status              Status
	OutstandingBalance  string // numeric(15,2)
	DueDay              int
	PaidOffDate         *time.Time
	LegacySource        *string
	CreatedAt           time.Time
}

// InstallmentStatus mirrors the installments.status CHECK constraint.
type InstallmentStatus string

const (
	InstallmentStatusPending   InstallmentStatus = "PENDING"
	InstallmentStatusPaid      InstallmentStatus = "PAID"
	InstallmentStatusOverdue   InstallmentStatus = "OVERDUE"
	InstallmentStatusCancelled InstallmentStatus = "CANCELLED"
)

// Installment mirrors the installments table. There's no direct Create/Update HTTP endpoint for
// it — installments are only ever produced by schedule generation (Service.Create) or the
// amortization recalculation (Service.ApplyAmortization), never authored freehand.
type Installment struct {
	ID                 uuid.UUID
	LoanID             uuid.UUID
	InstallmentNumber  int
	DueDate            time.Time
	InstallmentAmount  string
	OutstandingBalance string
	PrincipalAmount    string
	InterestAmount     string
	Status             InstallmentStatus
	CreatedAt          time.Time
}

// TransactionType mirrors the loan_transactions.type CHECK constraint — matches the old Java
// TipoMovimentacao 1:1 (DESEMBOLSO/RECEBIMENTO_PARCELA/RECEBIMENTO_AVULSO/PERDAO/AMORTIZACAO),
// already confirmed by Hermes to need no schema change (F-2 in docs/audit-20260809.md:
// loan_transactions IS the movimentacoes mapping, field-for-field).
type TransactionType string

const (
	// TransactionTypeDisbursement is DESEMBOLSO: recorded once, automatically, when a loan is
	// created (Service.Create) — never created directly through the transactions endpoint.
	TransactionTypeDisbursement TransactionType = "DISBURSEMENT"
	// TransactionTypeInstallmentPayment is RECEBIMENTO_PARCELA: recorded automatically by
	// Service.MarkInstallmentPaid — never created directly.
	TransactionTypeInstallmentPayment TransactionType = "INSTALLMENT_PAYMENT"
	// TransactionTypeOneOffPayment is RECEBIMENTO_AVULSO: a freeform payment not tied to a specific
	// installment — the old Java records this with NO automatic balance-reduction side effect
	// (confirmed reading MovimentacaoService.registrar — only AMORTIZACAO touches saldoDevedor),
	// so neither does this module; it's purely a ledger entry.
	TransactionTypeOneOffPayment TransactionType = "ONE_OFF_PAYMENT"
	// TransactionTypeForgiveness is PERDAO — same as ONE_OFF_PAYMENT, a ledger entry with no
	// automatic balance side effect in the real Java app.
	TransactionTypeForgiveness TransactionType = "FORGIVENESS"
	// TransactionTypeAmortization is AMORTIZACAO: recorded automatically by
	// Service.ApplyAmortization, which also reduces the loan's outstanding balance — never created
	// directly through the freeform transactions endpoint.
	TransactionTypeAmortization TransactionType = "AMORTIZATION"
)

// Transaction mirrors the loan_transactions table (movimentacoes). LoanID/InstallmentID are
// nullable FKs with ON DELETE SET NULL, same as the schema.
type Transaction struct {
	ID            uuid.UUID
	ClientID      uuid.UUID
	LoanID        *uuid.UUID
	InstallmentID *uuid.UUID
	Type          TransactionType
	Amount        string
	PaymentDate   time.Time
	Description   *string
	LegacySource  *string
	CreatedAt     time.Time
}

// LoanBalanceUpdate is the loan-side effect of a payment/amortization — bundled into one value so
// the repository can apply the installment change, the balance change, and the ledger entry
// together in a single transaction (see Repository's doc comment).
type LoanBalanceUpdate struct {
	LoanID      uuid.UUID
	NewBalance  string
	NewStatus   Status
	PaidOffDate *time.Time // non-nil only when NewStatus transitions to StatusPaidOff
}
