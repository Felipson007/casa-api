package loans

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	loans        map[uuid.UUID]Loan
	installments map[uuid.UUID]Installment // keyed by installment ID, not loan ID
	transactions map[uuid.UUID]Transaction
	clientNames  map[uuid.UUID]string // clientID -> name, for ListRelevantForVigentes
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		loans:        make(map[uuid.UUID]Loan),
		installments: make(map[uuid.UUID]Installment),
		transactions: make(map[uuid.UUID]Transaction),
		clientNames:  make(map[uuid.UUID]string),
	}
}

func (f *fakeRepository) List(_ context.Context, clientID *uuid.UUID, _ string) ([]Loan, error) {
	var result []Loan
	for _, l := range f.loans {
		if clientID != nil && l.ClientID != *clientID {
			continue
		}
		result = append(result, l)
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Loan, error) {
	l, ok := f.loans[id]
	if !ok {
		return nil, nil
	}
	return &l, nil
}

func (f *fakeRepository) CreateWithSchedule(_ context.Context, l Loan, installments []Installment, disbursement Transaction) error {
	f.loans[l.ID] = l
	for _, i := range installments {
		f.installments[i.ID] = i
	}
	f.transactions[disbursement.ID] = disbursement
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.loans, id)
	return nil
}

func (f *fakeRepository) HasPaidInstallment(_ context.Context, loanID uuid.UUID) (bool, error) {
	for _, i := range f.installments {
		if i.LoanID == loanID && i.Status == InstallmentStatusPaid {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepository) HasTransactionBeyondDisbursement(_ context.Context, loanID uuid.UUID) (bool, error) {
	for _, t := range f.transactions {
		if t.LoanID != nil && *t.LoanID == loanID && t.Type != TransactionTypeDisbursement {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepository) ListInstallmentsByLoan(_ context.Context, loanID uuid.UUID) ([]Installment, error) {
	var result []Installment
	for _, i := range f.installments {
		if i.LoanID == loanID {
			result = append(result, i)
		}
	}
	// Map iteration order is random — sort by installment number to match the real
	// postgres.Repository's ORDER BY installment_number, since RolloverRecurringInterest relies on
	// the last element being the highest-numbered installment.
	sort.Slice(result, func(i, j int) bool { return result[i].InstallmentNumber < result[j].InstallmentNumber })
	return result, nil
}

func (f *fakeRepository) FindInstallmentByID(_ context.Context, id uuid.UUID) (*Installment, error) {
	i, ok := f.installments[id]
	if !ok {
		return nil, nil
	}
	return &i, nil
}

func (f *fakeRepository) ListOpenInstallmentsAfter(_ context.Context, loanID uuid.UUID, after time.Time) ([]Installment, error) {
	var result []Installment
	for _, i := range f.installments {
		if i.LoanID == loanID && (i.Status == InstallmentStatusPending || i.Status == InstallmentStatusOverdue) && i.DueDate.After(after) {
			result = append(result, i)
		}
	}
	return result, nil
}

func (f *fakeRepository) MarkInstallmentPaid(_ context.Context, installmentID uuid.UUID, loanUpdate *LoanBalanceUpdate, txn Transaction) error {
	i, ok := f.installments[installmentID]
	if !ok {
		return errors.New("not found")
	}
	i.Status = InstallmentStatusPaid
	f.installments[installmentID] = i
	if loanUpdate != nil {
		l := f.loans[loanUpdate.LoanID]
		l.OutstandingBalance = loanUpdate.NewBalance
		l.Status = loanUpdate.NewStatus
		l.PaidOffDate = loanUpdate.PaidOffDate
		f.loans[loanUpdate.LoanID] = l
	}
	f.transactions[txn.ID] = txn
	return nil
}

func (f *fakeRepository) ApplyAmortization(_ context.Context, loanUpdate LoanBalanceUpdate, deleteInstallmentIDs []uuid.UUID, recalculated []Installment, txn Transaction) error {
	l := f.loans[loanUpdate.LoanID]
	l.OutstandingBalance = loanUpdate.NewBalance
	l.Status = loanUpdate.NewStatus
	l.PaidOffDate = loanUpdate.PaidOffDate
	f.loans[loanUpdate.LoanID] = l
	for _, id := range deleteInstallmentIDs {
		delete(f.installments, id)
	}
	for _, i := range recalculated {
		f.installments[i.ID] = i
	}
	f.transactions[txn.ID] = txn
	return nil
}

func (f *fakeRepository) RecordTransaction(_ context.Context, t Transaction) error {
	f.transactions[t.ID] = t
	return nil
}

func (f *fakeRepository) FindTransactionByID(_ context.Context, id uuid.UUID) (*Transaction, error) {
	t, ok := f.transactions[id]
	if !ok {
		return nil, nil
	}
	return &t, nil
}

func (f *fakeRepository) ListTransactionsByLoan(_ context.Context, loanID uuid.UUID) ([]Transaction, error) {
	var result []Transaction
	for _, t := range f.transactions {
		if t.LoanID != nil && *t.LoanID == loanID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (f *fakeRepository) ListAllTransactions(_ context.Context, clientID *uuid.UUID) ([]Transaction, error) {
	var result []Transaction
	for _, t := range f.transactions {
		if clientID != nil && t.ClientID != *clientID {
			continue
		}
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PaymentDate.After(result[j].PaymentDate) })
	return result, nil
}

func (f *fakeRepository) ListActiveRecurringInterestLoans(_ context.Context) ([]Loan, error) {
	var result []Loan
	for _, l := range f.loans {
		if l.Status == StatusActive && l.PaymentType == PaymentTypeRecurringInterest {
			result = append(result, l)
		}
	}
	return result, nil
}

func (f *fakeRepository) ExtendRecurringSchedule(_ context.Context, loanID uuid.UUID, newInstallments []Installment, newInstallmentCount int) error {
	for _, i := range newInstallments {
		f.installments[i.ID] = i
	}
	l := f.loans[loanID]
	l.InstallmentCount = newInstallmentCount
	f.loans[loanID] = l
	return nil
}

func (f *fakeRepository) ListRelevantForVigentes(_ context.Context) ([]LoanWithClientName, error) {
	var result []LoanWithClientName
	for _, l := range f.loans {
		if l.Status == StatusActive || l.Status == StatusPaidOff {
			result = append(result, LoanWithClientName{Loan: l, ClientName: f.clientNames[l.ClientID]})
		}
	}
	return result, nil
}

func (f *fakeRepository) ListInstallmentsDueInRange(_ context.Context, loanIDs []uuid.UUID, from, toExclusive time.Time) ([]Installment, error) {
	wanted := make(map[uuid.UUID]bool, len(loanIDs))
	for _, id := range loanIDs {
		wanted[id] = true
	}
	var result []Installment
	for _, i := range f.installments {
		if wanted[i.LoanID] && !i.DueDate.Before(from) && i.DueDate.Before(toExclusive) {
			result = append(result, i)
		}
	}
	return result, nil
}

func (f *fakeRepository) ListInstallmentPaymentsByDueDate(_ context.Context, from, toExclusive time.Time) ([]LoanAmountRow, error) {
	sums := make(map[uuid.UUID]map[YearMonth]string)
	for _, t := range f.transactions {
		if t.Type != TransactionTypeInstallmentPayment || t.InstallmentID == nil || t.LoanID == nil {
			continue
		}
		inst, ok := f.installments[*t.InstallmentID]
		if !ok || inst.DueDate.Before(from) || !inst.DueDate.Before(toExclusive) {
			continue
		}
		m := YearMonth{Year: inst.DueDate.Year(), Month: int(inst.DueDate.Month())}
		if sums[*t.LoanID] == nil {
			sums[*t.LoanID] = make(map[YearMonth]string)
		}
		sum, err := addMoney(orZero(sums[*t.LoanID][m]), t.Amount)
		if err != nil {
			return nil, err
		}
		sums[*t.LoanID][m] = sum
	}
	return flattenLoanAmountRows(sums), nil
}

func (f *fakeRepository) ListRecentTransactions(_ context.Context, limit int) ([]TransactionWithClientName, error) {
	var result []TransactionWithClientName
	for _, t := range f.transactions {
		result = append(result, TransactionWithClientName{Transaction: t, ClientName: f.clientNames[t.ClientID]})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (f *fakeRepository) SumOneOffAndAmortizationByMonth(_ context.Context, from, toExclusive time.Time) (map[YearMonth]string, map[YearMonth]string, error) {
	oneOff := make(map[YearMonth]string)
	amortization := make(map[YearMonth]string)
	for _, t := range f.transactions {
		if t.LoanID == nil || t.PaymentDate.Before(from) || !t.PaymentDate.Before(toExclusive) {
			continue
		}
		m := YearMonth{Year: t.PaymentDate.Year(), Month: int(t.PaymentDate.Month())}
		switch t.Type {
		case TransactionTypeAmortization:
			sum, err := addMoney(orZero(amortization[m]), t.Amount)
			if err != nil {
				return nil, nil, err
			}
			amortization[m] = sum
		case TransactionTypeOneOffPayment:
			sum, err := addMoney(orZero(oneOff[m]), t.Amount)
			if err != nil {
				return nil, nil, err
			}
			oneOff[m] = sum
		}
	}
	return oneOff, amortization, nil
}

func (f *fakeRepository) ListOtherPaymentsByPaymentDate(_ context.Context, from, toExclusive time.Time) ([]LoanAmountRow, error) {
	sums := make(map[uuid.UUID]map[YearMonth]string)
	for _, t := range f.transactions {
		if (t.Type != TransactionTypeAmortization && t.Type != TransactionTypeOneOffPayment) || t.LoanID == nil {
			continue
		}
		if t.PaymentDate.Before(from) || !t.PaymentDate.Before(toExclusive) {
			continue
		}
		m := YearMonth{Year: t.PaymentDate.Year(), Month: int(t.PaymentDate.Month())}
		if sums[*t.LoanID] == nil {
			sums[*t.LoanID] = make(map[YearMonth]string)
		}
		sum, err := addMoney(orZero(sums[*t.LoanID][m]), t.Amount)
		if err != nil {
			return nil, err
		}
		sums[*t.LoanID][m] = sum
	}
	return flattenLoanAmountRows(sums), nil
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func flattenLoanAmountRows(sums map[uuid.UUID]map[YearMonth]string) []LoanAmountRow {
	var result []LoanAmountRow
	for loanID, byMonth := range sums {
		for month, amount := range byMonth {
			result = append(result, LoanAmountRow{LoanID: loanID, Month: month, Amount: amount})
		}
	}
	return result
}

func (f *fakeRepository) UpdateFull(_ context.Context, l Loan, installments []Installment, disbursement Transaction) error {
	for id, i := range f.installments {
		if i.LoanID == l.ID {
			delete(f.installments, id)
		}
	}
	for id, t := range f.transactions {
		if t.LoanID != nil && *t.LoanID == l.ID {
			delete(f.transactions, id)
		}
	}
	f.loans[l.ID] = l
	for _, i := range installments {
		f.installments[i.ID] = i
	}
	f.transactions[disbursement.ID] = disbursement
	return nil
}

func (f *fakeRepository) Cancel(_ context.Context, loanID uuid.UUID) error {
	l := f.loans[loanID]
	l.Status = StatusCanceled
	f.loans[loanID] = l
	return nil
}

func (f *fakeRepository) ChangeDueDay(_ context.Context, loanID uuid.UUID, newDueDay int, shiftedInstallments []Installment) error {
	l := f.loans[loanID]
	l.DueDay = newDueDay
	f.loans[loanID] = l
	for _, inst := range shiftedInstallments {
		f.installments[inst.ID] = inst
	}
	return nil
}

func (f *fakeRepository) PostponeInstallment(_ context.Context, installmentID uuid.UUID, newDueDate time.Time) error {
	i := f.installments[installmentID]
	i.DueDate = newDueDate
	i.Status = InstallmentStatusPending
	f.installments[installmentID] = i
	return nil
}

func (f *fakeRepository) ChangeInterestRate(_ context.Context, loanID uuid.UUID, newRate string, recalculated []Installment) error {
	l := f.loans[loanID]
	l.MonthlyInterestRate = newRate
	f.loans[loanID] = l
	for _, inst := range recalculated {
		f.installments[inst.ID] = inst
	}
	return nil
}

func TestService_CreateGetList(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	clientID := uuid.New()

	created, err := service.Create(ctx, CreateLoanRequest{
		ClientID:            clientID,
		PrincipalAmount:     "1000.00",
		MonthlyInterestRate: "2.500000",
		PaymentType:         PaymentTypeInstallment,
		InstallmentCount:    12,
		ContractDate:        civildate.Of(time.Now()),
		DueDay:              5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Status != StatusActive {
		t.Fatalf("expected a newly created loan to be ACTIVE, got %v", created.Status)
	}
	if created.OutstandingBalance != "1000.00" {
		t.Fatalf("expected outstanding balance to start equal to principal, got %v", created.OutstandingBalance)
	}
	if created.InstallmentCount != 12 {
		t.Fatalf("expected installmentCount 12, got %d", created.InstallmentCount)
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ClientID != clientID {
		t.Fatalf("expected client ID %v, got %v", clientID, got.ClientID)
	}

	list, err := service.List(ctx, &clientID, "")
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 loan, got %d", len(list))
	}

	transactions, err := service.ListTransactions(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListTransactions returned error: %v", err)
	}
	if len(transactions) != 1 || transactions[0].Type != TransactionTypeDisbursement {
		t.Fatalf("expected 1 DISBURSEMENT transaction from Create, got %+v", transactions)
	}
}

func TestService_Get_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.Get(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	err := service.Delete(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Delete_BlockedAfterPayment(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 3, ContractDate: civildate.Of(time.Now()), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	installments, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	if _, err := service.MarkInstallmentPaid(ctx, loan.ID, installments[0].ID, MarkInstallmentPaidRequest{}); err != nil {
		t.Fatalf("MarkInstallmentPaid returned error: %v", err)
	}

	err = service.Delete(ctx, loan.ID)
	if !isValidationErr(err) {
		t.Fatalf("expected a validation error blocking delete after a payment, got %v", err)
	}
}

func TestService_ListInstallments_LoanNotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.ListInstallments(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Create_GeneratesFixedSchedule(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateLoanRequest{
		ClientID:            uuid.New(),
		PrincipalAmount:     "500.00",
		MonthlyInterestRate: "1.000000",
		PaymentType:         PaymentTypeInstallment,
		InstallmentCount:    6,
		ContractDate:        civildate.Of(time.Now()),
		DueDay:              10,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	list, err := service.ListInstallments(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	if len(list) != 6 {
		t.Fatalf("expected 6 generated installments, got %d", len(list))
	}
	for _, i := range list {
		if i.Status != InstallmentStatusPending {
			t.Fatalf("expected a freshly generated installment to be PENDING, got %v", i.Status)
		}
	}
}

func TestService_Create_RecurringInterest_GeneratesChargesThroughYearEnd(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	contractDate := date(2026, time.October, 1)

	created, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "2000.00", MonthlyInterestRate: "5",
		PaymentType: PaymentTypeRecurringInterest, ContractDate: civildate.Of(contractDate), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.OutstandingBalance != "2000.00" {
		t.Fatalf("expected outstanding balance to start at principal, got %v", created.OutstandingBalance)
	}

	list, err := service.ListInstallments(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	// Nov + Dec 2026 = 2 charges.
	if len(list) != 2 {
		t.Fatalf("expected 2 charges through year-end, got %d", len(list))
	}
	for _, i := range list {
		if i.PrincipalAmount != "0.00" {
			t.Fatalf("expected Principal 0.00 for a recurring charge, got %s", i.PrincipalAmount)
		}
	}
}

func TestService_MarkInstallmentPaid_ReducesBalanceForInstallmentLoans(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "0", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 3, ContractDate: civildate.Of(time.Now()), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	installments, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}

	updated, err := service.MarkInstallmentPaid(ctx, loan.ID, installments[0].ID, MarkInstallmentPaidRequest{})
	if err != nil {
		t.Fatalf("MarkInstallmentPaid returned error: %v", err)
	}
	if updated.Status != InstallmentStatusPaid {
		t.Fatalf("expected installment status PAID, got %v", updated.Status)
	}

	reloaded, err := service.Get(ctx, loan.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if reloaded.OutstandingBalance != "200.00" {
		t.Fatalf("expected outstanding balance reduced by the first installment's principal (100.00), got %s", reloaded.OutstandingBalance)
	}
	if reloaded.Status != StatusActive {
		t.Fatalf("expected loan to remain ACTIVE after 1 of 3 installments paid, got %v", reloaded.Status)
	}

	// Paying the remaining installments should reach PAID_OFF exactly when the last is paid.
	if _, err := service.MarkInstallmentPaid(ctx, loan.ID, installments[1].ID, MarkInstallmentPaidRequest{}); err != nil {
		t.Fatalf("MarkInstallmentPaid (2nd) returned error: %v", err)
	}
	if _, err := service.MarkInstallmentPaid(ctx, loan.ID, installments[2].ID, MarkInstallmentPaidRequest{}); err != nil {
		t.Fatalf("MarkInstallmentPaid (3rd) returned error: %v", err)
	}
	final, err := service.Get(ctx, loan.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if final.Status != StatusPaidOff || final.OutstandingBalance != "0.00" || final.PaidOffDate == nil {
		t.Fatalf("expected loan PAID_OFF with 0.00 balance and a paidOffDate after the last installment, got %+v", final)
	}

	if _, err := service.MarkInstallmentPaid(ctx, loan.ID, installments[0].ID, MarkInstallmentPaidRequest{}); !isValidationErr(err) {
		t.Fatalf("expected a validation error re-paying an already-paid installment, got %v", err)
	}
}

// TestService_MarkInstallmentPaid_AcceptsDateOnlyPaymentDateFromJSON is a regression test for a real
// bug: the frontend sends paymentDate as a bare "YYYY-MM-DD" string (civildate's canonical format),
// which used to fail json.Unmarshal because PaymentDate was typed *time.Time (requires full RFC3339)
// instead of *civildate.Date — every other date-only DTO field already used civildate.Date. Decodes
// JSON exactly like httpx.DecodeAndValidate would, to catch a regression at the wire-format boundary,
// not just at the Go-struct level (which the request literals used elsewhere in this file skip past).
func TestService_MarkInstallmentPaid_AcceptsDateOnlyPaymentDateFromJSON(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "0", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 3, ContractDate: civildate.Of(time.Now()), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	installments, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}

	var req MarkInstallmentPaidRequest
	if err := json.Unmarshal([]byte(`{"paymentDate":"2026-08-11","amountReceived":null}`), &req); err != nil {
		t.Fatalf("unmarshal returned error: %v", err)
	}
	if req.PaymentDate == nil {
		t.Fatal("expected PaymentDate to be set after unmarshal")
	}
	wantDate := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	if !req.PaymentDate.Time().Equal(wantDate) {
		t.Fatalf("expected PaymentDate %v, got %v", wantDate, req.PaymentDate.Time())
	}

	updated, err := service.MarkInstallmentPaid(ctx, loan.ID, installments[0].ID, req)
	if err != nil {
		t.Fatalf("MarkInstallmentPaid returned error: %v", err)
	}
	if updated.Status != InstallmentStatusPaid {
		t.Fatalf("expected installment status PAID, got %v", updated.Status)
	}
}

func TestService_MarkInstallmentPaid_DoesNotAffectBalanceForRecurringInterestLoans(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "1000.00", MonthlyInterestRate: "5",
		PaymentType: PaymentTypeRecurringInterest, ContractDate: civildate.Of(date(2026, time.October, 1)), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	installments, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}

	if _, err := service.MarkInstallmentPaid(ctx, loan.ID, installments[0].ID, MarkInstallmentPaidRequest{}); err != nil {
		t.Fatalf("MarkInstallmentPaid returned error: %v", err)
	}

	reloaded, err := service.Get(ctx, loan.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if reloaded.OutstandingBalance != "1000.00" {
		t.Fatalf("expected outstanding balance UNCHANGED for a recurring-interest charge payment, got %s", reloaded.OutstandingBalance)
	}
}

func TestService_ApplyAmortization_ReducesBalanceAndRecalculatesFutureCharges(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	contractDate := date(2026, time.October, 1)

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "1000.00", MonthlyInterestRate: "5",
		PaymentType: PaymentTypeRecurringInterest, ContractDate: civildate.Of(contractDate), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	updated, err := service.ApplyAmortization(ctx, loan.ID, ApplyAmortizationRequest{
		Amount: "400.00", PaymentDate: civildate.Of(date(2026, time.October, 15)),
	})
	if err != nil {
		t.Fatalf("ApplyAmortization returned error: %v", err)
	}
	if updated.OutstandingBalance != "600.00" {
		t.Fatalf("expected outstanding balance 600.00 after a 400.00 amortization, got %s", updated.OutstandingBalance)
	}
	if updated.Status != StatusActive {
		t.Fatalf("expected loan to remain ACTIVE, got %v", updated.Status)
	}

	installments, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	for _, i := range installments {
		// 600.00 * 5% = 30.00 — every future recurring charge must reflect the new balance.
		if i.InterestAmount != "30.00" || i.InstallmentAmount != "30.00" || i.OutstandingBalance != "600.00" {
			t.Fatalf("expected future charges recalculated to interest=amount=30.00 balance=600.00, got %+v", i)
		}
	}

	transactions, err := service.ListTransactions(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListTransactions returned error: %v", err)
	}
	found := false
	for _, txn := range transactions {
		if txn.Type == TransactionTypeAmortization && txn.Amount == "400.00" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an AMORTIZATION transaction for 400.00")
	}
}

func TestService_ApplyAmortization_FullPayoffDeletesOpenInstallments(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "1000.00", MonthlyInterestRate: "5",
		PaymentType: PaymentTypeRecurringInterest, ContractDate: civildate.Of(date(2026, time.October, 1)), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	updated, err := service.ApplyAmortization(ctx, loan.ID, ApplyAmortizationRequest{
		Amount: "1000.00", PaymentDate: civildate.Of(date(2026, time.October, 15)),
	})
	if err != nil {
		t.Fatalf("ApplyAmortization returned error: %v", err)
	}
	if updated.Status != StatusPaidOff || updated.OutstandingBalance != "0.00" {
		t.Fatalf("expected the loan fully PAID_OFF, got %+v", updated)
	}

	list, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected remaining open installments deleted after a full payoff, got %d", len(list))
	}
}

func TestService_ApplyAmortization_RejectsInactiveLoan(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "100.00", MonthlyInterestRate: "5",
		PaymentType: PaymentTypeRecurringInterest, ContractDate: civildate.Of(date(2026, time.October, 1)), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := service.ApplyAmortization(ctx, loan.ID, ApplyAmortizationRequest{Amount: "100.00", PaymentDate: civildate.Of(date(2026, time.October, 15))}); err != nil {
		t.Fatalf("ApplyAmortization (full payoff) returned error: %v", err)
	}

	_, err = service.ApplyAmortization(ctx, loan.ID, ApplyAmortizationRequest{Amount: "10.00", PaymentDate: civildate.Of(date(2026, time.November, 1))})
	if !isValidationErr(err) {
		t.Fatalf("expected a validation error amortizing an already-paid-off loan, got %v", err)
	}
}

func TestService_RecordTransaction_OneOffPaymentHasNoBalanceSideEffect(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "500.00", MonthlyInterestRate: "2", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 5, ContractDate: civildate.Of(time.Now()), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	txn, err := service.RecordTransaction(ctx, loan.ID, CreateTransactionRequest{
		Type: TransactionTypeOneOffPayment, Amount: "50.00", PaymentDate: civildate.Of(time.Now()),
	})
	if err != nil {
		t.Fatalf("RecordTransaction returned error: %v", err)
	}
	if txn.Type != TransactionTypeOneOffPayment {
		t.Fatalf("expected ONE_OFF_PAYMENT type, got %v", txn.Type)
	}

	reloaded, err := service.Get(ctx, loan.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if reloaded.OutstandingBalance != "500.00" {
		t.Fatalf("expected outstanding balance UNCHANGED by a ONE_OFF_PAYMENT (matches the real Java behavior), got %s", reloaded.OutstandingBalance)
	}
}

// TestService_GetTransaction_Found covers the payment-receipt PDF's lookup path
// (internal/reports.GenerateTransactionReceiptPDF) — a single transaction fetched by ID rather than
// listed.
func TestService_GetTransaction_Found(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "500.00", MonthlyInterestRate: "2", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 5, ContractDate: civildate.Of(time.Now()), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	created, err := service.RecordTransaction(ctx, loan.ID, CreateTransactionRequest{
		Type: TransactionTypeOneOffPayment, Amount: "50.00", PaymentDate: civildate.Of(time.Now()),
	})
	if err != nil {
		t.Fatalf("RecordTransaction returned error: %v", err)
	}

	got, err := service.GetTransaction(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTransaction returned error: %v", err)
	}
	if got.ID != created.ID || got.Amount != "50.00" || got.Type != TransactionTypeOneOffPayment {
		t.Fatalf("expected the recorded transaction back, got %+v", got)
	}
}

func TestService_GetTransaction_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)

	_, err := service.GetTransaction(context.Background(), uuid.New())
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_ListAllTransactions_FiltersByClientAndSortsDescending(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	clientA, clientB := uuid.New(), uuid.New()
	loanA, err := service.Create(ctx, CreateLoanRequest{
		ClientID: clientA, PrincipalAmount: "500.00", MonthlyInterestRate: "2", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 5, ContractDate: civildate.Of(time.Now()), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create loanA returned error: %v", err)
	}
	loanB, err := service.Create(ctx, CreateLoanRequest{
		ClientID: clientB, PrincipalAmount: "300.00", MonthlyInterestRate: "2", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 3, ContractDate: civildate.Of(time.Now()), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create loanB returned error: %v", err)
	}

	older := time.Now().AddDate(0, 0, -10)
	newer := time.Now()
	if _, err := service.RecordTransaction(ctx, loanA.ID, CreateTransactionRequest{
		Type: TransactionTypeOneOffPayment, Amount: "10.00", PaymentDate: civildate.Of(older),
	}); err != nil {
		t.Fatalf("RecordTransaction (loanA, older) returned error: %v", err)
	}
	if _, err := service.RecordTransaction(ctx, loanA.ID, CreateTransactionRequest{
		Type: TransactionTypeOneOffPayment, Amount: "20.00", PaymentDate: civildate.Of(newer),
	}); err != nil {
		t.Fatalf("RecordTransaction (loanA, newer) returned error: %v", err)
	}
	if _, err := service.RecordTransaction(ctx, loanB.ID, CreateTransactionRequest{
		Type: TransactionTypeOneOffPayment, Amount: "30.00", PaymentDate: civildate.Of(newer),
	}); err != nil {
		t.Fatalf("RecordTransaction (loanB) returned error: %v", err)
	}

	// Create itself records a DISBURSEMENT transaction per loan, on top of the ones recorded above:
	// loanA has 1 disbursement + 2 one-off payments = 3; loanB has 1 disbursement + 1 one-off = 2.
	all, err := service.ListAllTransactions(ctx, nil)
	if err != nil {
		t.Fatalf("ListAllTransactions(nil) returned error: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("expected all 5 transactions (2 disbursements + 3 recorded) across both loans in one call, got %d", len(all))
	}
	for i := 0; i < len(all)-1; i++ {
		if all[i].PaymentDate.Time().Before(all[i+1].PaymentDate.Time()) {
			t.Fatalf("expected transactions sorted most-recent-first, got %+v", all)
		}
	}

	onlyA, err := service.ListAllTransactions(ctx, &clientA)
	if err != nil {
		t.Fatalf("ListAllTransactions(clientA) returned error: %v", err)
	}
	if len(onlyA) != 3 {
		t.Fatalf("expected only clientA's 3 transactions (1 disbursement + 2 one-off), got %d", len(onlyA))
	}
	for _, txn := range onlyA {
		if txn.ClientID != clientA {
			t.Fatalf("expected every returned transaction to belong to clientA, got %+v", txn)
		}
	}
}

func isValidationErr(err error) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Kind == apperr.KindValidation
}

func TestService_RolloverRecurringInterest_ExtendsIntoNewYear(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "1000.00", MonthlyInterestRate: "5",
		PaymentType: PaymentTypeRecurringInterest, ContractDate: civildate.Of(date(2025, time.November, 1)), DueDay: 10,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	// The first charge falls 1 month after contractDate (Nov 1 -> Dec 1) — only Dec 2025 falls in
	// 2025, so exactly 1 installment is generated at creation.
	before, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("expected 1 installment right after Create, got %d", len(before))
	}

	if err := service.RolloverRecurringInterest(ctx, date(2026, time.January, 15)); err != nil {
		t.Fatalf("RolloverRecurringInterest returned error: %v", err)
	}

	after, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	if len(after) != 13 { // 1 existing + 12 months of 2026
		t.Fatalf("expected 13 installments after rollover into 2026, got %d", len(after))
	}
	last := after[len(after)-1]
	if last.InstallmentNumber != 13 || !last.DueDate.Time().Equal(date(2026, time.December, 10)) {
		t.Fatalf("expected the last installment to be #13 due Dec 10 2026, got #%d due %v", last.InstallmentNumber, last.DueDate)
	}

	reloadedLoan, err := service.Get(ctx, loan.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if reloadedLoan.InstallmentCount != 13 {
		t.Fatalf("expected loan.InstallmentCount updated to 13, got %d", reloadedLoan.InstallmentCount)
	}
}

// TestService_RolloverRecurringInterest_IsIdempotent is exactly Hermes's stated acceptance
// criterion (chat msg 91): running the rollover twice in a row for the same reference date must
// produce the identical set of installments, never duplicates.
func TestService_RolloverRecurringInterest_IsIdempotent(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "1000.00", MonthlyInterestRate: "5",
		PaymentType: PaymentTypeRecurringInterest, ContractDate: civildate.Of(date(2025, time.November, 1)), DueDay: 10,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	referenceDate := date(2026, time.January, 15)
	if err := service.RolloverRecurringInterest(ctx, referenceDate); err != nil {
		t.Fatalf("RolloverRecurringInterest (1st run) returned error: %v", err)
	}
	firstRun, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}

	if err := service.RolloverRecurringInterest(ctx, referenceDate); err != nil {
		t.Fatalf("RolloverRecurringInterest (2nd run, same day) returned error: %v", err)
	}
	secondRun, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}

	if len(firstRun) != len(secondRun) {
		t.Fatalf("expected the same number of installments after a repeat rollover, got %d then %d", len(firstRun), len(secondRun))
	}
	for i := range firstRun {
		if firstRun[i] != secondRun[i] {
			t.Fatalf("expected installment %d unchanged after a repeat rollover, got %+v then %+v", i, firstRun[i], secondRun[i])
		}
	}

	// A later run within the SAME target year must also be a no-op (not just the exact same day).
	if err := service.RolloverRecurringInterest(ctx, referenceDate.AddDate(0, 2, 0)); err != nil {
		t.Fatalf("RolloverRecurringInterest (3rd run, later same year) returned error: %v", err)
	}
	thirdRun, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	if len(thirdRun) != len(firstRun) {
		t.Fatalf("expected no new installments from a later run within the same target year, got %d (was %d)", len(thirdRun), len(firstRun))
	}
}

func TestService_RolloverRecurringInterest_SkipsInstallmentLoans(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 3, ContractDate: civildate.Of(date(2025, time.November, 1)), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	before, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}

	if err := service.RolloverRecurringInterest(ctx, date(2027, time.January, 1)); err != nil {
		t.Fatalf("RolloverRecurringInterest returned error: %v", err)
	}

	after, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("expected an INSTALLMENT loan's schedule untouched by rollover, got %d (was %d)", len(after), len(before))
	}
}

func TestService_Cancel(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "1000.00", MonthlyInterestRate: "5", PaymentType: PaymentTypeRecurringInterest,
		ContractDate: civildate.Of(date(2026, time.January, 1)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	cancelled, err := service.Cancel(ctx, loan.ID)
	if err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	if cancelled.Status != StatusCanceled {
		t.Fatalf("expected CANCELLED, got %s", cancelled.Status)
	}
}

func TestService_Cancel_BlocksAlreadyPaidOff(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "500.00", MonthlyInterestRate: "2", PaymentType: PaymentTypeRecurringInterest,
		ContractDate: civildate.Of(date(2026, time.January, 1)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	l := repo.loans[loan.ID]
	l.Status = StatusPaidOff
	repo.loans[loan.ID] = l

	if _, err := service.Cancel(ctx, loan.ID); err == nil {
		t.Fatal("expected an error cancelling an already-paid-off loan")
	}
}

func TestService_ChangeDueDay_ClampsToMonthLength(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "900.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 3, ContractDate: civildate.Of(date(2026, time.January, 5)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	updated, err := service.ChangeDueDay(ctx, loan.ID, 31)
	if err != nil {
		t.Fatalf("ChangeDueDay returned error: %v", err)
	}
	if updated.DueDay != 31 {
		t.Fatalf("expected loan.DueDay = 31, got %d", updated.DueDay)
	}

	installments, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	for _, inst := range installments {
		dueDate := inst.DueDate.Time()
		lastDay := time.Date(dueDate.Year(), dueDate.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		if dueDate.Day() != lastDay {
			t.Fatalf("installment #%d: expected day-of-month clamped to %d, got %d", inst.InstallmentNumber, lastDay, dueDate.Day())
		}
	}
}

func TestService_PostponeInstallment_MovesSingleInstallment(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "600.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 2, ContractDate: civildate.Of(date(2026, time.January, 5)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	installments, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	first := installments[0]

	newDueDate := date(2026, time.April, 5)
	updated, err := service.PostponeInstallment(ctx, loan.ID, first.ID, newDueDate)
	if err != nil {
		t.Fatalf("PostponeInstallment returned error: %v", err)
	}
	if updated.ID != loan.ID {
		t.Fatalf("expected the same loan returned, got %+v", updated)
	}

	after, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	if !after[0].DueDate.Time().Equal(newDueDate) {
		t.Fatalf("expected installment #1 postponed to %v, got %v", newDueDate, after[0].DueDate.Time())
	}
	if !after[1].DueDate.Time().Equal(installments[1].DueDate.Time()) {
		t.Fatalf("expected installment #2 untouched, got %v", after[1].DueDate.Time())
	}
}

// Reproduz o caso real que motivou este fix: uma parcela já OVERDUE (o job diário de manutenção
// só marca PENDING -> OVERDUE, nunca reverte) precisa voltar pra PENDING ao ser adiada pro futuro —
// senão ela fica exibida como "atrasada" pra sempre mesmo não estando mais vencida.
func TestService_PostponeInstallment_ResetsOverdueStatusToPending(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 1, ContractDate: civildate.Of(date(2026, time.January, 5)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	installments, _ := service.ListInstallments(ctx, loan.ID)
	inst := repo.installments[installments[0].ID]
	inst.Status = InstallmentStatusOverdue
	repo.installments[inst.ID] = inst

	if _, err := service.PostponeInstallment(ctx, loan.ID, inst.ID, date(2026, time.March, 5)); err != nil {
		t.Fatalf("PostponeInstallment returned error: %v", err)
	}

	if got := repo.installments[inst.ID].Status; got != InstallmentStatusPending {
		t.Fatalf("expected status reset to PENDING after postponing an OVERDUE installment, got %s", got)
	}
}

func TestService_PostponeInstallment_RejectsPaidInstallment(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 1, ContractDate: civildate.Of(date(2026, time.January, 5)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	installments, _ := service.ListInstallments(ctx, loan.ID)
	inst := repo.installments[installments[0].ID]
	inst.Status = InstallmentStatusPaid
	repo.installments[inst.ID] = inst

	if _, err := service.PostponeInstallment(ctx, loan.ID, inst.ID, date(2026, time.March, 5)); err == nil {
		t.Fatal("expected an error postponing a PAID installment")
	}
}

func TestService_PostponeInstallment_RejectsCancelledInstallment(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 1, ContractDate: civildate.Of(date(2026, time.January, 5)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	installments, _ := service.ListInstallments(ctx, loan.ID)
	inst := repo.installments[installments[0].ID]
	inst.Status = InstallmentStatusCancelled
	repo.installments[inst.ID] = inst

	if _, err := service.PostponeInstallment(ctx, loan.ID, inst.ID, date(2026, time.March, 5)); err == nil {
		t.Fatal("expected an error postponing a CANCELLED installment")
	}
}

func TestService_PostponeInstallment_RejectsInactiveLoan(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 1, ContractDate: civildate.Of(date(2026, time.January, 5)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := service.Cancel(ctx, loan.ID); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	installments, _ := service.ListInstallments(ctx, loan.ID)

	if _, err := service.PostponeInstallment(ctx, loan.ID, installments[0].ID, date(2026, time.March, 5)); err == nil {
		t.Fatal("expected an error postponing an installment on a non-ACTIVE loan")
	}
}

func TestService_PostponeInstallment_RejectsPastOrSameDate(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 1, ContractDate: civildate.Of(date(2026, time.January, 5)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	installments, _ := service.ListInstallments(ctx, loan.ID)
	current := installments[0].DueDate.Time()

	if _, err := service.PostponeInstallment(ctx, loan.ID, installments[0].ID, current); err == nil {
		t.Fatal("expected an error postponing to the same due date")
	}
	if _, err := service.PostponeInstallment(ctx, loan.ID, installments[0].ID, current.AddDate(0, 0, -1)); err == nil {
		t.Fatal("expected an error postponing to a date before the current due date")
	}
}

// This is the collision the Vigentes month-map assumption (vigentes.go) can't tolerate: two open
// installments of the same loan landing in the same calendar month, one of them silently dropped
// from that view. PostponeInstallment must reject before it happens instead of failing silently.
func TestService_PostponeInstallment_RejectsCollisionWithAnotherOpenInstallmentInTargetMonth(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "900.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 3, ContractDate: civildate.Of(date(2026, time.January, 5)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	installments, _ := service.ListInstallments(ctx, loan.ID)
	first := installments[0]
	thirdDueDate := installments[2].DueDate.Time()

	if _, err := service.PostponeInstallment(ctx, loan.ID, first.ID, thirdDueDate); err == nil {
		t.Fatal("expected an error postponing into a month another open installment already occupies")
	}
}

func TestService_PostponeInstallment_RejectsInstallmentFromDifferentLoan(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loanA, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 1, ContractDate: civildate.Of(date(2026, time.January, 5)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	loanB, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 1, ContractDate: civildate.Of(date(2026, time.January, 5)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	installmentsB, _ := service.ListInstallments(ctx, loanB.ID)

	if _, err := service.PostponeInstallment(ctx, loanA.ID, installmentsB[0].ID, date(2026, time.March, 5)); err == nil {
		t.Fatal("expected an error postponing an installment that belongs to a different loan")
	}
}

func TestService_Update_RegeneratesSchedule(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "1000.00", MonthlyInterestRate: "2", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 5, ContractDate: civildate.Of(date(2026, time.January, 1)), DueDay: 10,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	updated, err := service.Update(ctx, loan.ID, CreateLoanRequest{
		ClientID: loan.ClientID, PrincipalAmount: "2000.00", MonthlyInterestRate: "3", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 10, ContractDate: civildate.Of(date(2026, time.February, 1)), DueDay: 15,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.PrincipalAmount != "2000.00" || updated.InstallmentCount != 10 {
		t.Fatalf("expected the new terms applied, got %+v", updated)
	}

	installments, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	if len(installments) != 10 {
		t.Fatalf("expected 10 regenerated installments, got %d", len(installments))
	}
}

func TestService_Update_BlockedAfterPayment(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "300.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 3, ContractDate: civildate.Of(date(2026, time.January, 1)), DueDay: 10,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	installments, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments returned error: %v", err)
	}
	if _, err := service.MarkInstallmentPaid(ctx, loan.ID, installments[0].ID, MarkInstallmentPaidRequest{}); err != nil {
		t.Fatalf("MarkInstallmentPaid returned error: %v", err)
	}

	if _, err := service.Update(ctx, loan.ID, CreateLoanRequest{
		ClientID: loan.ClientID, PrincipalAmount: "999.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 3, ContractDate: civildate.Of(date(2026, time.January, 1)), DueDay: 10,
	}); err == nil {
		t.Fatal("expected Update to be blocked once a payment is recorded")
	}
}

func TestService_Vigentes_PaidOffOnlyAppearsInPayoffMonth(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	clientID := uuid.New()
	repo.clientNames[clientID] = "Test Client"

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: clientID, PrincipalAmount: "500.00", MonthlyInterestRate: "2", PaymentType: PaymentTypeRecurringInterest,
		ContractDate: civildate.Of(date(2026, time.January, 1)), DueDay: 5,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	paidOff := date(2026, time.March, 15)
	l := repo.loans[loan.ID]
	l.Status = StatusPaidOff
	l.PaidOffDate = &paidOff
	repo.loans[loan.ID] = l

	byMonth, err := service.Vigentes(ctx, []YearMonth{{Year: 2026, Month: 3}, {Year: 2026, Month: 4}})
	if err != nil {
		t.Fatalf("Vigentes returned error: %v", err)
	}
	if len(byMonth[YearMonth{2026, 3}]) != 1 || byMonth[YearMonth{2026, 3}][0].Status != MonthStatusPaidOff {
		t.Fatalf("expected the loan to appear PAID_OFF in its payoff month, got %+v", byMonth[YearMonth{2026, 3}])
	}
	if len(byMonth[YearMonth{2026, 4}]) != 0 {
		t.Fatalf("expected a PAID_OFF loan to NOT appear in months after its payoff month, got %+v", byMonth[YearMonth{2026, 4}])
	}
}

func TestService_Vigentes_OverdueByPastDueDate(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	clientID := uuid.New()
	repo.clientNames[clientID] = "Late Payer"

	// A far-past contract date guarantees every installment's due_date is already in the past
	// relative to time.Now(), so the "still PENDING but due_date before today" branch fires.
	if _, err := service.Create(ctx, CreateLoanRequest{
		ClientID: clientID, PrincipalAmount: "300.00", MonthlyInterestRate: "1", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 3, ContractDate: civildate.Of(date(2020, time.January, 1)), DueDay: 5,
	}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	byMonth, err := service.Vigentes(ctx, []YearMonth{{Year: 2020, Month: 2}})
	if err != nil {
		t.Fatalf("Vigentes returned error: %v", err)
	}
	results := byMonth[YearMonth{2020, 2}]
	if len(results) != 1 || results[0].Status != MonthStatusOverdue {
		t.Fatalf("expected exactly one OVERDUE entry for the first installment's month, got %+v", results)
	}
}

func TestService_CalculateKPIs_InstallmentLoanUsesFixedPrincipalForCapitalAtRisk(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	clientID := uuid.New()
	repo.clientNames[clientID] = "Test Client"

	if _, err := service.Create(ctx, CreateLoanRequest{
		ClientID: clientID, PrincipalAmount: "1200.00", MonthlyInterestRate: "2", PaymentType: PaymentTypeInstallment,
		InstallmentCount: 3, ContractDate: civildate.Of(date(2026, time.January, 1)), DueDay: 10,
	}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	byMonth, err := service.CalculateKPIs(ctx, []YearMonth{{Year: 2026, Month: 2}})
	if err != nil {
		t.Fatalf("CalculateKPIs returned error: %v", err)
	}
	kpi := byMonth[YearMonth{2026, 2}]
	// PARCELADO: capitalAtRisk uses the loan's fixed principal (1200.00), not a per-installment
	// snapshot — same value every month regardless of how much has amortized.
	if kpi.CapitalAtRisk != "1200.00" {
		t.Fatalf("expected capitalAtRisk = fixed principal 1200.00, got %s", kpi.CapitalAtRisk)
	}
	if kpi.ActiveThisMonth != 1 {
		t.Fatalf("expected 1 active installment this month, got %d", kpi.ActiveThisMonth)
	}
	if kpi.AverageRate != "2.00" {
		t.Fatalf("expected averageRate 2.00, got %s", kpi.AverageRate)
	}
}

func TestService_CalculateKPIs_RecurringInterestUsesInstallmentBalanceSnapshot(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	clientID := uuid.New()
	repo.clientNames[clientID] = "Test Client"

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: clientID, PrincipalAmount: "5000.00", MonthlyInterestRate: "3", PaymentType: PaymentTypeRecurringInterest,
		ContractDate: civildate.Of(date(2026, time.January, 1)), DueDay: 10,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	// Amortize down to 3000.00 before February — capitalAtRisk for a RECURRING_INTEREST loan should
	// reflect February's actual snapshot (3000.00), not the original principal (5000.00).
	if _, err := service.ApplyAmortization(ctx, loan.ID, ApplyAmortizationRequest{Amount: "2000.00", PaymentDate: civildate.Of(date(2026, time.January, 20))}); err != nil {
		t.Fatalf("ApplyAmortization returned error: %v", err)
	}

	byMonth, err := service.CalculateKPIs(ctx, []YearMonth{{Year: 2026, Month: 2}})
	if err != nil {
		t.Fatalf("CalculateKPIs returned error: %v", err)
	}
	kpi := byMonth[YearMonth{2026, 2}]
	if kpi.CapitalAtRisk != "3000.00" {
		t.Fatalf("expected capitalAtRisk = the balance snapshot after amortization (3000.00), got %s", kpi.CapitalAtRisk)
	}
}
