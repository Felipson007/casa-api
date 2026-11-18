package loans

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
//
// Create's FK violation (client_id not found) is translated to *apperr.Error by the postgres
// implementation, not checked here — see internal/loans/postgres/repository.go.
//
// Several methods bundle more than one write into a single call (CreateWithSchedule,
// MarkInstallmentPaid, ApplyAmortization) — each corresponds to one old Java @Transactional method
// that touched more than one table atomically (loan + installments + ledger entry); splitting
// them into separate Repository calls would reopen a partial-failure window the Java never had —
// same reasoning as internal/finance.Repository.Update taking an optional snapshot.
type Repository interface {
	List(ctx context.Context, clientID *uuid.UUID, query string) ([]Loan, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Loan, error)
	// CreateWithSchedule persists the loan header, its generated installments, and the initiating
	// DISBURSEMENT transaction, all in one transaction.
	CreateWithSchedule(ctx context.Context, l Loan, installments []Installment, disbursement Transaction) error
	Delete(ctx context.Context, id uuid.UUID) error
	// HasPaidInstallment and HasTransactionBeyondDisbursement guard Delete — mirrors
	// EmprestimoService.garantirSemAtividadeFinanceira: a loan with any real payment/transaction
	// history can no longer be deleted outright.
	HasPaidInstallment(ctx context.Context, loanID uuid.UUID) (bool, error)
	HasTransactionBeyondDisbursement(ctx context.Context, loanID uuid.UUID) (bool, error)

	ListInstallmentsByLoan(ctx context.Context, loanID uuid.UUID) ([]Installment, error)
	FindInstallmentByID(ctx context.Context, id uuid.UUID) (*Installment, error)
	// ListOpenInstallmentsAfter returns PENDING/OVERDUE installments due strictly after `after` —
	// used by ApplyAmortization to find which RECURRING_INTEREST charges need recalculating, and
	// (passing the zero time.Time, which every real due date is after) to find every open
	// installment when a loan becomes fully paid off early.
	ListOpenInstallmentsAfter(ctx context.Context, loanID uuid.UUID, after time.Time) ([]Installment, error)

	// MarkInstallmentPaid marks an installment PAID, optionally updates the loan's balance/status
	// (nil loanUpdate means "don't touch the loan" — used for RECURRING_INTEREST, which doesn't
	// amortize on installment payment), and records the payment transaction.
	MarkInstallmentPaid(ctx context.Context, installmentID uuid.UUID, loanUpdate *LoanBalanceUpdate, txn Transaction) error
	// ApplyAmortization updates the loan's balance/status, deletes deleteInstallmentIDs (used when
	// the loan becomes fully paid off), updates recalculated installments' interest/amount/balance
	// (used for RECURRING_INTEREST loans that remain active), and records the transaction.
	ApplyAmortization(ctx context.Context, loanUpdate LoanBalanceUpdate, deleteInstallmentIDs []uuid.UUID, recalculated []Installment, txn Transaction) error
	// RecordTransaction persists a freeform ledger entry with no other side effect (used for
	// ONE_OFF_PAYMENT/FORGIVENESS — see TransactionType's doc comments).
	RecordTransaction(ctx context.Context, t Transaction) error
	// ChangeInterestRate updates the loan's monthly_interest_rate and rewrites the recalculated
	// (still-open) installments' interest/amount, in one transaction. Mirrors ChangeDueDay's shape.
	ChangeInterestRate(ctx context.Context, loanID uuid.UUID, newRate string, recalculated []Installment) error
	ListTransactionsByLoan(ctx context.Context, loanID uuid.UUID) ([]Transaction, error)
	// FindTransactionByID looks up a single transaction — backs the payment-receipt PDF
	// (internal/reports), the one place a transaction is fetched on its own rather than listed.
	FindTransactionByID(ctx context.Context, id uuid.UUID) (*Transaction, error)
	// ListAllTransactions is the flat, cross-loan ledger — one query (optionally filtered to a
	// single client), most recent first — backing GET /api/transactions. Exists specifically so
	// the frontend's movimentacoesApi.list/listRecentes never has to fan out one HTTP call per
	// loan (it used to: fetch every loan, then Promise.all one /transactions call each — hundreds
	// of requests once the loan table has years of history, not just the handful active today).
	ListAllTransactions(ctx context.Context, clientID *uuid.UUID) ([]Transaction, error)

	// ListActiveRecurringInterestLoans and ExtendRecurringSchedule back RolloverRecurringInterest
	// (below) — the daily rollover job (wired into cmd/worker's scheduler by Hermes, per D-005;
	// this package only exposes the pure operation, no ticker/cron of its own).
	ListActiveRecurringInterestLoans(ctx context.Context) ([]Loan, error)
	// ExtendRecurringSchedule persists newly generated installments and updates the loan's
	// installment_count, in one transaction.
	ExtendRecurringSchedule(ctx context.Context, loanID uuid.UUID, newInstallments []Installment, newInstallmentCount int) error

	// The four methods below back Vigentes (vigentes.go) — see its own doc comments for exactly
	// what each returns and why (mirrors EmprestimoVigentesService's three underlying queries).
	ListRelevantForVigentes(ctx context.Context) ([]LoanWithClientName, error)
	ListInstallmentsDueInRange(ctx context.Context, loanIDs []uuid.UUID, from, toExclusive time.Time) ([]Installment, error)
	ListInstallmentPaymentsByDueDate(ctx context.Context, from, toExclusive time.Time) ([]LoanAmountRow, error)
	ListOtherPaymentsByPaymentDate(ctx context.Context, from, toExclusive time.Time) ([]LoanAmountRow, error)
	// SumOneOffAndAmortizationByMonth backs CalculateKPIs (kpis.go) — same underlying data as
	// ListOtherPaymentsByPaymentDate, but split by type instead of combined, since the KPI formula
	// needs amortizacoesNoMes as its own field, distinct from recebidoNoMes.
	SumOneOffAndAmortizationByMonth(ctx context.Context, from, toExclusive time.Time) (oneOff, amortization map[YearMonth]string, err error)

	// ListRecentTransactions backs the dashboard's "recent activity" feed — every loan_transaction
	// across every loan, most recent first, with the client's name pre-resolved.
	ListRecentTransactions(ctx context.Context, limit int) ([]TransactionWithClientName, error)

	// UpdateFull, Cancel back loans_lifecycle.go's Update/Cancel — mirrors
	// EmprestimoService.atualizar/cancelar: UpdateFull deletes every existing installment AND
	// transaction for the loan, updates the loan row, and inserts the freshly generated
	// installments + a new disbursement transaction, all in one transaction (same reasoning as
	// CreateWithSchedule — see that method's doc comment).
	UpdateFull(ctx context.Context, l Loan, installments []Installment, disbursement Transaction) error
	Cancel(ctx context.Context, loanID uuid.UUID) error
	// ChangeDueDay updates the loan's due_day and shifts every still-PENDING installment's due_date
	// to the same day-of-month going forward — see loans_lifecycle.go's ChangeDueDay.
	ChangeDueDay(ctx context.Context, loanID uuid.UUID, newDueDay int, shiftedInstallments []Installment) error
	// PostponeInstallment updates a single installment's due_date, leaving every other installment
	// (and the loan's own due_day) untouched — see loans_lifecycle.go's PostponeInstallment.
	PostponeInstallment(ctx context.Context, installmentID uuid.UUID, newDueDate time.Time) error
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13) except Create, which is O(installmentCount) in-memory
// (schedule generation) but still a single database round trip via CreateWithSchedule.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns loans, optionally filtered to a single client.
func (s *Service) List(ctx context.Context, clientID *uuid.UUID, query string) ([]LoanDTO, error) {
	list, err := s.repo.List(ctx, clientID, query)
	if err != nil {
		return nil, fmt.Errorf("loans: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single loan, or apperr.NotFound if it doesn't exist.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (LoanDTO, error) {
	l, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: looking up: %w", err)
	}
	if l == nil {
		return LoanDTO{}, apperr.NotFound("loan not found")
	}
	return toDTO(*l), nil
}

// Create persists a new loan, generates its installment schedule (fixed up front for
// PaymentTypeInstallment, first-year batch for PaymentTypeRecurringInterest — see schedule.go),
// and records the initiating DISBURSEMENT transaction — all in one transaction. OutstandingBalance
// always starts equal to PrincipalAmount and Status always starts ACTIVE.
func (s *Service) Create(ctx context.Context, req CreateLoanRequest) (LoanDTO, error) {
	monthsToFirst := 1
	if req.SkipFirstMonth {
		monthsToFirst = 2
	}

	var calcs []InstallmentCalc
	var err error
	switch req.PaymentType {
	case PaymentTypeInstallment:
		calcs, err = GenerateFixedSchedule(req.PrincipalAmount, req.MonthlyInterestRate, req.InstallmentCount, req.ContractDate.Time(), req.DueDay, monthsToFirst)
	case PaymentTypeRecurringInterest:
		calcs, err = GenerateRecurringChargesThroughYearEnd(req.PrincipalAmount, req.MonthlyInterestRate, req.ContractDate.Time(), req.DueDay, monthsToFirst)
	}
	if err != nil {
		return LoanDTO{}, err
	}

	loanID := uuid.New()
	l := Loan{
		ID:                  loanID,
		ClientID:            req.ClientID,
		PrincipalAmount:     req.PrincipalAmount,
		MonthlyInterestRate: req.MonthlyInterestRate,
		PaymentType:         req.PaymentType,
		InstallmentCount:    len(calcs),
		ContractDate:        req.ContractDate.Time(),
		Status:              StatusActive,
		OutstandingBalance:  req.PrincipalAmount,
		DueDay:              req.DueDay,
		LegacySource:        req.LegacySource,
	}

	installments := make([]Installment, len(calcs))
	for i, c := range calcs {
		installments[i] = Installment{
			ID:                 uuid.New(),
			LoanID:             loanID,
			InstallmentNumber:  c.Number,
			DueDate:            c.DueDate,
			InstallmentAmount:  c.Amount,
			OutstandingBalance: c.OutstandingBalance,
			PrincipalAmount:    c.Principal,
			InterestAmount:     c.Interest,
			Status:             InstallmentStatusPending,
		}
	}

	description := "Loan disbursement."
	disbursement := Transaction{
		ID:          uuid.New(),
		ClientID:    req.ClientID,
		LoanID:      &loanID,
		Type:        TransactionTypeDisbursement,
		Amount:      req.PrincipalAmount,
		PaymentDate: req.ContractDate.Time(),
		Description: &description,
	}

	if err := s.repo.CreateWithSchedule(ctx, l, installments, disbursement); err != nil {
		return LoanDTO{}, err // already an *apperr.Error (FK violation) or a wrapped DB error
	}

	// Re-read rather than mapping the in-memory `l` directly: CreatedAt is a DB-generated
	// DEFAULT now() (never set above) and MonthlyInterestRate is echoed back at whatever scale the
	// caller sent it, not the numeric(12,6) canonical form Postgres actually stores — e.g. a
	// caller sending "10" would get "10" back here instead of "10.000000" (found by Hermes's HTTP
	// endpoint sweep, msg 83 / F-3). GET immediately after Create already returned the correct
	// values; this makes POST's response consistent with it instead of a second, slightly wrong
	// source of truth.
	persisted, err := s.repo.FindByID(ctx, loanID)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: re-reading loan after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// Delete removes a loan by ID, or returns apperr.NotFound if it doesn't exist. Blocked
// (apperr.Validation) if the loan has any paid installment or any transaction beyond its own
// initiating disbursement — mirrors EmprestimoService.garantirSemAtividadeFinanceira: editing/
// deleting real payment history would corrupt it. installments/loan_transactions reference loans
// with ON DELETE CASCADE/SET NULL respectively (000001_init_schema.up.sql).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("loans: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("loan not found")
	}

	hasPaid, err := s.repo.HasPaidInstallment(ctx, id)
	if err != nil {
		return fmt.Errorf("loans: checking paid installments for delete: %w", err)
	}
	hasActivity, err := s.repo.HasTransactionBeyondDisbursement(ctx, id)
	if err != nil {
		return fmt.Errorf("loans: checking transactions for delete: %w", err)
	}
	if hasPaid || hasActivity {
		return apperr.Validation("this loan already has payments or transactions recorded and can no longer be deleted")
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("loans: deleting: %w", err)
	}
	return nil
}

// ListInstallments returns a loan's installments, or apperr.NotFound if the loan itself doesn't
// exist (as opposed to existing with zero installments, which returns an empty slice).
func (s *Service) ListInstallments(ctx context.Context, loanID uuid.UUID) ([]InstallmentDTO, error) {
	loan, err := s.repo.FindByID(ctx, loanID)
	if err != nil {
		return nil, fmt.Errorf("loans: looking up loan for installments: %w", err)
	}
	if loan == nil {
		return nil, apperr.NotFound("loan not found")
	}

	installments, err := s.repo.ListInstallmentsByLoan(ctx, loanID)
	if err != nil {
		return nil, fmt.Errorf("loans: listing installments: %w", err)
	}
	return toInstallmentDTOs(installments), nil
}

// MarkInstallmentPaid marks an installment PAID, reduces the loan's outstanding balance for
// PaymentTypeInstallment loans (never for PaymentTypeRecurringInterest, which only amortizes
// explicitly), and records an INSTALLMENT_PAYMENT transaction for the amount actually received —
// mirrors ParcelaService.marcarComoPaga exactly, including its defaults (full InstallmentAmount,
// today) when the request omits amount/date.
func (s *Service) MarkInstallmentPaid(ctx context.Context, loanID, installmentID uuid.UUID, req MarkInstallmentPaidRequest) (InstallmentDTO, error) {
	installment, err := s.repo.FindInstallmentByID(ctx, installmentID)
	if err != nil {
		return InstallmentDTO{}, fmt.Errorf("loans: looking up installment: %w", err)
	}
	if installment == nil || installment.LoanID != loanID {
		return InstallmentDTO{}, apperr.NotFound("installment not found")
	}
	if installment.Status == InstallmentStatusPaid {
		return InstallmentDTO{}, apperr.Validation("this installment is already paid")
	}

	loan, err := s.repo.FindByID(ctx, loanID)
	if err != nil {
		return InstallmentDTO{}, fmt.Errorf("loans: looking up loan: %w", err)
	}
	if loan == nil {
		return InstallmentDTO{}, apperr.NotFound("loan not found")
	}

	paymentDate := time.Now()
	if req.PaymentDate != nil {
		paymentDate = req.PaymentDate.Time()
	}
	amountReceived := installment.InstallmentAmount
	if req.AmountReceived != nil {
		amountReceived = *req.AmountReceived
	}

	var loanUpdate *LoanBalanceUpdate
	if loan.PaymentType == PaymentTypeInstallment {
		newBalance, newStatus, paidOffDate, err := reduceBalance(loan.OutstandingBalance, installment.PrincipalAmount, paymentDate)
		if err != nil {
			return InstallmentDTO{}, err
		}
		loanUpdate = &LoanBalanceUpdate{LoanID: loanID, NewBalance: newBalance, NewStatus: newStatus, PaidOffDate: paidOffDate}
	}

	description := fmt.Sprintf("Payment of installment #%d.", installment.InstallmentNumber)
	txn := Transaction{
		ID:            uuid.New(),
		ClientID:      loan.ClientID,
		LoanID:        &loanID,
		InstallmentID: &installmentID,
		Type:          TransactionTypeInstallmentPayment,
		Amount:        amountReceived,
		PaymentDate:   paymentDate,
		Description:   &description,
	}

	if err := s.repo.MarkInstallmentPaid(ctx, installmentID, loanUpdate, txn); err != nil {
		return InstallmentDTO{}, err
	}
	installment.Status = InstallmentStatusPaid
	return toInstallmentDTO(*installment), nil
}

// ApplyAmortization applies a principal paydown: reduces the loan's outstanding balance, and —
// unless the loan is now fully paid off (in which case remaining open installments are deleted) —
// for PaymentTypeRecurringInterest loans, recalculates interest on installments due after
// req.PaymentDate to reflect the new, lower balance. Records an AMORTIZATION transaction. Mirrors
// EmprestimoRecorrenteService.aplicarAmortizacao exactly.
func (s *Service) ApplyAmortization(ctx context.Context, loanID uuid.UUID, req ApplyAmortizationRequest) (LoanDTO, error) {
	loan, err := s.repo.FindByID(ctx, loanID)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: looking up loan: %w", err)
	}
	if loan == nil {
		return LoanDTO{}, apperr.NotFound("loan not found")
	}
	if loan.Status != StatusActive {
		return LoanDTO{}, apperr.Validation(fmt.Sprintf("this loan is not active (status %s) — cannot record an amortization", loan.Status))
	}

	newBalance, newStatus, paidOffDate, err := reduceBalance(loan.OutstandingBalance, req.Amount, req.PaymentDate.Time())
	if err != nil {
		return LoanDTO{}, err
	}
	loanUpdate := LoanBalanceUpdate{LoanID: loanID, NewBalance: newBalance, NewStatus: newStatus, PaidOffDate: paidOffDate}

	var deleteIDs []uuid.UUID
	var recalculated []Installment
	switch {
	case newStatus == StatusPaidOff:
		open, err := s.repo.ListOpenInstallmentsAfter(ctx, loanID, time.Time{})
		if err != nil {
			return LoanDTO{}, fmt.Errorf("loans: listing open installments for payoff cleanup: %w", err)
		}
		for _, inst := range open {
			deleteIDs = append(deleteIDs, inst.ID)
		}
	case loan.PaymentType == PaymentTypeRecurringInterest:
		open, err := s.repo.ListOpenInstallmentsAfter(ctx, loanID, req.PaymentDate.Time())
		if err != nil {
			return LoanDTO{}, fmt.Errorf("loans: listing open installments for recalculation: %w", err)
		}
		for _, inst := range open {
			interest, amount, balance, err := RecalculateRecurringCharge(newBalance, loan.MonthlyInterestRate)
			if err != nil {
				return LoanDTO{}, err
			}
			inst.InterestAmount = interest
			inst.InstallmentAmount = amount
			inst.OutstandingBalance = balance
			recalculated = append(recalculated, inst)
		}
	}

	txn := Transaction{
		ID:          uuid.New(),
		ClientID:    loan.ClientID,
		LoanID:      &loanID,
		Type:        TransactionTypeAmortization,
		Amount:      req.Amount,
		PaymentDate: req.PaymentDate.Time(),
		Description: req.Description,
	}

	if err := s.repo.ApplyAmortization(ctx, loanUpdate, deleteIDs, recalculated, txn); err != nil {
		return LoanDTO{}, err
	}

	updated, err := s.repo.FindByID(ctx, loanID)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: re-reading loan after amortization: %w", err)
	}
	return toDTO(*updated), nil
}

// RecordTransaction persists a freeform ONE_OFF_PAYMENT/FORGIVENESS ledger entry — no automatic
// balance side effect, matching the real Java's MovimentacaoService.registrar (only AMORTIZACAO
// touches saldoDevedor there).
func (s *Service) RecordTransaction(ctx context.Context, loanID uuid.UUID, req CreateTransactionRequest) (TransactionDTO, error) {
	loan, err := s.repo.FindByID(ctx, loanID)
	if err != nil {
		return TransactionDTO{}, fmt.Errorf("loans: looking up loan: %w", err)
	}
	if loan == nil {
		return TransactionDTO{}, apperr.NotFound("loan not found")
	}

	txn := Transaction{
		ID:          uuid.New(),
		ClientID:    loan.ClientID,
		LoanID:      &loanID,
		Type:        req.Type,
		Amount:      req.Amount,
		PaymentDate: req.PaymentDate.Time(),
		Description: req.Description,
	}
	if err := s.repo.RecordTransaction(ctx, txn); err != nil {
		return TransactionDTO{}, fmt.Errorf("loans: recording transaction: %w", err)
	}
	return toTransactionDTO(txn), nil
}

// ListTransactions returns a loan's transaction ledger, or apperr.NotFound if the loan itself
// doesn't exist.
func (s *Service) ListTransactions(ctx context.Context, loanID uuid.UUID) ([]TransactionDTO, error) {
	loan, err := s.repo.FindByID(ctx, loanID)
	if err != nil {
		return nil, fmt.Errorf("loans: looking up loan for transactions: %w", err)
	}
	if loan == nil {
		return nil, apperr.NotFound("loan not found")
	}
	list, err := s.repo.ListTransactionsByLoan(ctx, loanID)
	if err != nil {
		return nil, fmt.Errorf("loans: listing transactions: %w", err)
	}
	return toTransactionDTOs(list), nil
}

// GetTransaction returns a single transaction, or apperr.NotFound if it doesn't exist — backs the
// payment-receipt PDF (internal/reports), which needs one transaction plus (if LoanID is set) its
// parent loan and client, not a whole ledger listing.
func (s *Service) GetTransaction(ctx context.Context, id uuid.UUID) (TransactionDTO, error) {
	t, err := s.repo.FindTransactionByID(ctx, id)
	if err != nil {
		return TransactionDTO{}, fmt.Errorf("loans: looking up transaction: %w", err)
	}
	if t == nil {
		return TransactionDTO{}, apperr.NotFound("transaction not found")
	}
	return toTransactionDTO(*t), nil
}

// ListAllTransactions returns the flat, cross-loan transaction ledger, most recent first,
// optionally filtered to one client — one round trip regardless of how many loans exist.
func (s *Service) ListAllTransactions(ctx context.Context, clientID *uuid.UUID) ([]TransactionDTO, error) {
	list, err := s.repo.ListAllTransactions(ctx, clientID)
	if err != nil {
		return nil, fmt.Errorf("loans: listing all transactions: %w", err)
	}
	return toTransactionDTOs(list), nil
}

// RolloverRecurringInterest extends every ACTIVE RECURRING_INTEREST loan's installment schedule
// once its last generated installment falls in a year before referenceDate's — mirrors
// EmprestimoRecorrenteService.garantirParcelasAteFimDoAno as called by the old Java's daily
// EmprestimoMaintenanceScheduler.estenderEmprestimosRecorrentes. Pure domain operation with no
// scheduling of its own: calling this on a cron/ticker is cmd/worker's job (Hermes-owned per
// D-005), not this package's.
//
// Idempotent: calling it twice for the same referenceDate (or any date in the same target year)
// is a no-op the second time — each loan is only extended if its last installment's due-date year
// is strictly before the target year, and after extending, that's no longer true. Safe to retry
// or to run more than once a day.
func (s *Service) RolloverRecurringInterest(ctx context.Context, referenceDate time.Time) error {
	activeLoans, err := s.repo.ListActiveRecurringInterestLoans(ctx)
	if err != nil {
		return fmt.Errorf("loans: listing active recurring-interest loans: %w", err)
	}

	for _, loan := range activeLoans {
		installments, err := s.repo.ListInstallmentsByLoan(ctx, loan.ID)
		if err != nil {
			return fmt.Errorf("loans: listing installments for loan %s: %w", loan.ID, err)
		}

		var newCalcs []InstallmentCalc
		if len(installments) == 0 {
			// Defensive — Create always generates at least the first batch; this only matters if
			// that invariant is ever broken.
			newCalcs, err = GenerateRecurringChargesThroughYearEnd(loan.OutstandingBalance, loan.MonthlyInterestRate, loan.ContractDate, loan.DueDay, 1)
		} else {
			last := installments[len(installments)-1]
			if last.DueDate.Year() >= referenceDate.Year() {
				continue // already extended far enough — makes a repeat call a no-op
			}
			newCalcs, err = ContinueRecurringSchedule(last.InstallmentNumber, last.DueDate, loan.OutstandingBalance, loan.MonthlyInterestRate, loan.DueDay)
		}
		if err != nil {
			return err
		}
		if len(newCalcs) == 0 {
			continue
		}

		newInstallments := make([]Installment, len(newCalcs))
		for i, c := range newCalcs {
			newInstallments[i] = Installment{
				ID: uuid.New(), LoanID: loan.ID, InstallmentNumber: c.Number, DueDate: c.DueDate,
				InstallmentAmount: c.Amount, OutstandingBalance: c.OutstandingBalance,
				PrincipalAmount: c.Principal, InterestAmount: c.Interest, Status: InstallmentStatusPending,
			}
		}
		if err := s.repo.ExtendRecurringSchedule(ctx, loan.ID, newInstallments, loan.InstallmentCount+len(newInstallments)); err != nil {
			return fmt.Errorf("loans: extending schedule for loan %s: %w", loan.ID, err)
		}
	}
	return nil
}
