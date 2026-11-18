package loans

import (
	"fmt"
	"time"

	"context"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Part 3 continued: the three lifecycle operations domain.go's part-2 doc comment explicitly
// deferred — Update (full replacement), Cancel, and ChangeDueDay — read from
// EmprestimoService.atualizar/cancelar/EmprestimoRecorrenteService.alterarDiaVencimento rather than
// guessed.

// Update replaces a loan's terms outright: deletes every existing installment and transaction and
// regenerates the schedule from scratch (same schedule-generation path as Create), resetting status
// to ACTIVE and clearing any paid-off date. Guarded exactly like Delete — a loan with any real
// payment history can't have its terms rewritten (mirrors
// EmprestimoService.atualizar/garantirSemAtividadeFinanceira).
func (s *Service) Update(ctx context.Context, id uuid.UUID, req CreateLoanRequest) (LoanDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: looking up for update: %w", err)
	}
	if existing == nil {
		return LoanDTO{}, apperr.NotFound("loan not found")
	}
	hasPaid, err := s.repo.HasPaidInstallment(ctx, id)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: checking paid installments for update: %w", err)
	}
	hasActivity, err := s.repo.HasTransactionBeyondDisbursement(ctx, id)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: checking transactions for update: %w", err)
	}
	if hasPaid || hasActivity {
		return LoanDTO{}, apperr.Validation("this loan already has payments or transactions recorded and can no longer have its terms rewritten — cancel it instead")
	}

	monthsToFirst := 1
	if req.SkipFirstMonth {
		monthsToFirst = 2
	}
	var calcs []InstallmentCalc
	switch req.PaymentType {
	case PaymentTypeInstallment:
		calcs, err = GenerateFixedSchedule(req.PrincipalAmount, req.MonthlyInterestRate, req.InstallmentCount, req.ContractDate.Time(), req.DueDay, monthsToFirst)
	case PaymentTypeRecurringInterest:
		calcs, err = GenerateRecurringChargesThroughYearEnd(req.PrincipalAmount, req.MonthlyInterestRate, req.ContractDate.Time(), req.DueDay, monthsToFirst)
	}
	if err != nil {
		return LoanDTO{}, err
	}

	l := Loan{
		ID:                  id,
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
			ID: uuid.New(), LoanID: id, InstallmentNumber: c.Number, DueDate: c.DueDate,
			InstallmentAmount: c.Amount, OutstandingBalance: c.OutstandingBalance,
			PrincipalAmount: c.Principal, InterestAmount: c.Interest, Status: InstallmentStatusPending,
		}
	}
	description := "Loan disbursement (terms updated)."
	disbursement := Transaction{
		ID: uuid.New(), ClientID: req.ClientID, LoanID: &id,
		Type: TransactionTypeDisbursement, Amount: req.PrincipalAmount,
		PaymentDate: req.ContractDate.Time(), Description: &description,
	}

	if err := s.repo.UpdateFull(ctx, l, installments, disbursement); err != nil {
		return LoanDTO{}, err
	}
	updated, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: re-reading loan after update: %w", err)
	}
	return toDTO(*updated), nil
}

// Cancel is a soft, reversible-in-spirit encirclement for a loan that already has real payment
// history (unlike Delete/Update, nothing is removed — installments and transactions are left
// exactly as they are). Blocked if the loan is already PAID_OFF — mirrors
// EmprestimoService.cancelar exactly ("já está quitado — não faz sentido cancelar").
func (s *Service) Cancel(ctx context.Context, id uuid.UUID) (LoanDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: looking up for cancel: %w", err)
	}
	if existing == nil {
		return LoanDTO{}, apperr.NotFound("loan not found")
	}
	if existing.Status == StatusPaidOff {
		return LoanDTO{}, apperr.Validation("this loan is already paid off — cancelling makes no sense")
	}
	if err := s.repo.Cancel(ctx, id); err != nil {
		return LoanDTO{}, fmt.Errorf("loans: cancelling: %w", err)
	}
	updated, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: re-reading loan after cancel: %w", err)
	}
	return toDTO(*updated), nil
}

// ChangeDueDay updates which day-of-month a loan's installments fall due on, going forward: the
// loan's due_day itself changes, and every still-open (PENDING/OVERDUE) installment keeps its exact
// due month/year but moves to the new day — clamped to the target month's actual length (e.g. day
// 31 in a 30-day month becomes day 30) — mirrors
// EmprestimoRecorrenteService.alterarDiaVencimento exactly, including that clamp.
func (s *Service) ChangeDueDay(ctx context.Context, id uuid.UUID, newDueDay int) (LoanDTO, error) {
	if newDueDay < 1 || newDueDay > 31 {
		return LoanDTO{}, apperr.Validation("due day must be between 1 and 31", "dueDay")
	}
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: looking up for due-day change: %w", err)
	}
	if existing == nil {
		return LoanDTO{}, apperr.NotFound("loan not found")
	}

	open, err := s.repo.ListOpenInstallmentsAfter(ctx, id, time.Time{}) // zero time: every open installment, not just future ones
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: listing open installments for due-day change: %w", err)
	}
	shifted := make([]Installment, len(open))
	for i, inst := range open {
		year, month := inst.DueDate.Year(), inst.DueDate.Month()
		lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
		day := newDueDay
		if day > lastDay {
			day = lastDay
		}
		inst.DueDate = time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		shifted[i] = inst
	}

	if err := s.repo.ChangeDueDay(ctx, id, newDueDay, shifted); err != nil {
		return LoanDTO{}, fmt.Errorf("loans: changing due day: %w", err)
	}
	updated, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: re-reading loan after due-day change: %w", err)
	}
	return toDTO(*updated), nil
}

// PostponeInstallment moves a single still-open installment's due date forward — e.g. giving one
// borrower a one-off grace month — without touching any other installment on the loan. Unlike
// ChangeDueDay (which shifts every open installment by the same day-of-month, keeping each one's
// month/year fixed), this targets exactly one installment and can cross into a different month.
//
// The month-collision check below exists because Vigentes (vigentes.go) assumes at most one open
// installment per loan per calendar month (installmentByLoanAndMonth) — silently dropping one of
// two installments that landed in the same month from that view, with no error. Postponing into a
// month another open installment already occupies would trip that assumption, so it's rejected
// here instead of failing silently downstream.
func (s *Service) PostponeInstallment(ctx context.Context, loanID, installmentID uuid.UUID, newDueDate time.Time) (LoanDTO, error) {
	loan, err := s.repo.FindByID(ctx, loanID)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: looking up for installment postponement: %w", err)
	}
	if loan == nil {
		return LoanDTO{}, apperr.NotFound("loan not found")
	}
	if loan.Status != StatusActive {
		return LoanDTO{}, apperr.Validation(fmt.Sprintf("this loan is not active (status %s) — its installments cannot be postponed", loan.Status))
	}

	inst, err := s.repo.FindInstallmentByID(ctx, installmentID)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: looking up installment for postponement: %w", err)
	}
	if inst == nil || inst.LoanID != loanID {
		return LoanDTO{}, apperr.NotFound("installment not found for this loan")
	}
	if inst.Status != InstallmentStatusPending && inst.Status != InstallmentStatusOverdue {
		return LoanDTO{}, apperr.Validation(fmt.Sprintf("installment is %s — only PENDING/OVERDUE installments can be postponed", inst.Status))
	}
	if !newDueDate.After(inst.DueDate) {
		return LoanDTO{}, apperr.Validation("the new due date must be after the installment's current due date", "newDueDate")
	}

	open, err := s.repo.ListOpenInstallmentsAfter(ctx, loanID, time.Time{})
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: listing open installments for postponement collision check: %w", err)
	}
	targetMonth := yearMonthOf(newDueDate)
	for _, other := range open {
		if other.ID == inst.ID {
			continue
		}
		if yearMonthOf(other.DueDate) == targetMonth {
			return LoanDTO{}, apperr.Validation("this loan already has an open installment due in that month — pick a different month")
		}
	}

	if err := s.repo.PostponeInstallment(ctx, installmentID, newDueDate); err != nil {
		return LoanDTO{}, fmt.Errorf("loans: postponing installment: %w", err)
	}
	updated, err := s.repo.FindByID(ctx, loanID)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: re-reading loan after installment postponement: %w", err)
	}
	return toDTO(*updated), nil
}

// ChangeInterestRate changes a loan's monthly interest rate going forward: PAID installments are
// left exactly as they were, every still-open (PENDING/OVERDUE) installment is recomputed at the
// new rate. Used when a rate is renegotiated with a client — e.g. dropping every loan of theirs
// to a lower rate as part of a deal.
//
// RECURRING_INTEREST only. For those the charge is simply balance * rate, so re-deriving each
// open installment via RecalculateRecurringCharge (the same helper amortization already uses) is
// exact and the outstanding balance snapshot per installment is untouched. INSTALLMENT (PARCELADO)
// loans amortize principal on a fixed schedule; re-rating one mid-loan is a schedule rebuild with
// its own choices (keep the payment? the count? re-amortize the remaining balance?) and is
// rejected here rather than guessed — recreate the loan, or edit it while it still has no payments.
//
// newRatePercent "0" is allowed: every open installment then becomes 0.00 and the loan stays
// ACTIVE (the principal is still owed; reduce it later with an amortization).
func (s *Service) ChangeInterestRate(ctx context.Context, id uuid.UUID, newRatePercent string) (LoanDTO, error) {
	rate, err := parseMoney(newRatePercent)
	if err != nil {
		return LoanDTO{}, apperr.Validation("monthlyInterestRate must be a valid decimal", "monthlyInterestRate")
	}
	if rate.Sign() < 0 {
		return LoanDTO{}, apperr.Validation("monthlyInterestRate cannot be negative", "monthlyInterestRate")
	}

	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: looking up for interest-rate change: %w", err)
	}
	if existing == nil {
		return LoanDTO{}, apperr.NotFound("loan not found")
	}
	if existing.Status != StatusActive {
		return LoanDTO{}, apperr.Validation(fmt.Sprintf("this loan is not active (status %s) — its rate cannot be changed", existing.Status))
	}
	if existing.PaymentType != PaymentTypeRecurringInterest {
		return LoanDTO{}, apperr.Validation("only recurring-interest loans support a rate change; recreate the loan or edit it while it has no payments")
	}

	newRate := rate.FloatString(6) // numeric(12,6) column; percent value e.g. "7.500000"

	open, err := s.repo.ListOpenInstallmentsAfter(ctx, id, time.Time{}) // zero time: every open installment
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: listing open installments for interest-rate change: %w", err)
	}
	recalculated := make([]Installment, 0, len(open))
	for _, inst := range open {
		interest, amount, balance, rErr := RecalculateRecurringCharge(inst.OutstandingBalance, newRate)
		if rErr != nil {
			return LoanDTO{}, rErr
		}
		inst.InterestAmount = interest
		inst.InstallmentAmount = amount
		inst.OutstandingBalance = balance
		recalculated = append(recalculated, inst)
	}

	if err := s.repo.ChangeInterestRate(ctx, id, newRate, recalculated); err != nil {
		return LoanDTO{}, fmt.Errorf("loans: changing interest rate: %w", err)
	}
	updated, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return LoanDTO{}, fmt.Errorf("loans: re-reading loan after interest-rate change: %w", err)
	}
	return toDTO(*updated), nil
}
