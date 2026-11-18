//go:build integration

package loans_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/loans/postgres"
	"github.com/atlas/atlas-go/internal/notifications"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// seedClient inserts a throwaway client row (loans.client_id is NOT NULL with a foreign key) and
// returns its parsed id, registering cleanup via t.Cleanup.
func seedClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var clientID string
	if err := pool.QueryRow(ctx, `INSERT INTO clients (name) VALUES ('Loan Test Client') RETURNING id`).Scan(&clientID); err != nil {
		t.Fatalf("seeding test client: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID) }) //nolint:errcheck
	parsed, err := uuid.Parse(clientID)
	if err != nil {
		t.Fatalf("parsing seeded client id: %v", err)
	}
	return parsed
}

// TestRepository_CreateGetListDelete exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/loans/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestRepository_CreateGetListDelete(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()

	clientID := seedClient(t, ctx, pool)
	repo := postgres.NewRepository(pool)
	service := loans.NewService(repo)

	created, err := service.Create(ctx, loans.CreateLoanRequest{
		ClientID:            clientID,
		PrincipalAmount:     "2000.00",
		MonthlyInterestRate: "3.500000",
		PaymentType:         loans.PaymentTypeInstallment,
		InstallmentCount:    10,
		ContractDate:        civildate.Of(time.Now()),
		DueDay:              15,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM loans WHERE id = $1`, created.ID) //nolint:errcheck

	if created.Status != loans.StatusActive {
		t.Fatalf("expected ACTIVE status, got %v", created.Status)
	}
	if created.OutstandingBalance != "2000.00" {
		t.Fatalf("expected outstanding balance 2000.00, got %v", created.OutstandingBalance)
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PrincipalAmount != "2000.00" {
		t.Fatalf("expected principal 2000.00 round-tripped through numeric(15,2), got %q", got.PrincipalAmount)
	}

	list, err := service.List(ctx, &clientID, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, l := range list {
		if l.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the just-created loan back from List, got %+v", list)
	}

	installments, err := service.ListInstallments(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListInstallments: %v", err)
	}
	if len(installments) != 10 {
		t.Fatalf("expected 10 generated installments, got %d", len(installments))
	}

	transactions, err := service.ListTransactions(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if len(transactions) != 1 || transactions[0].Type != loans.TransactionTypeDisbursement {
		t.Fatalf("expected 1 DISBURSEMENT transaction from Create, got %+v", transactions)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// TestRepository_Create_RejectsUnknownClient confirms the FK-violation -> apperr.Validation
// translation in postgres.Repository.CreateWithSchedule.
func TestRepository_Create_RejectsUnknownClient(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()

	repo := postgres.NewRepository(pool)
	service := loans.NewService(repo)

	_, err = service.Create(ctx, loans.CreateLoanRequest{
		ClientID:            uuid.New(),
		PrincipalAmount:     "100.00",
		MonthlyInterestRate: "1.000000",
		PaymentType:         loans.PaymentTypeInstallment,
		InstallmentCount:    1,
		ContractDate:        civildate.Of(time.Now()),
		DueDay:              1,
	})
	if err == nil {
		t.Fatal("expected an error for a nonexistent clientId")
	}
}

// TestRepository_MarkInstallmentPaid_ReducesBalanceAndPaysOff confirms the full payment cycle
// against real Postgres: paying every installment of an INSTALLMENT loan reduces its outstanding
// balance to zero and transitions it to PAID_OFF exactly on the last one.
func TestRepository_MarkInstallmentPaid_ReducesBalanceAndPaysOff(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()

	clientID := seedClient(t, ctx, pool)
	repo := postgres.NewRepository(pool)
	service := loans.NewService(repo)

	loan, err := service.Create(ctx, loans.CreateLoanRequest{
		ClientID: clientID, PrincipalAmount: "300.00", MonthlyInterestRate: "0",
		PaymentType: loans.PaymentTypeInstallment, InstallmentCount: 3, ContractDate: civildate.Of(time.Now()), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM loans WHERE id = $1`, loan.ID) //nolint:errcheck

	installments, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments: %v", err)
	}
	for i, inst := range installments {
		updated, err := service.MarkInstallmentPaid(ctx, loan.ID, inst.ID, loans.MarkInstallmentPaidRequest{})
		if err != nil {
			t.Fatalf("MarkInstallmentPaid (installment %d): %v", i+1, err)
		}
		if updated.Status != loans.InstallmentStatusPaid {
			t.Fatalf("expected installment %d PAID, got %v", i+1, updated.Status)
		}
	}

	final, err := service.Get(ctx, loan.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Status != loans.StatusPaidOff || final.OutstandingBalance != "0.00" || final.PaidOffDate == nil {
		t.Fatalf("expected the loan PAID_OFF with 0.00 balance and a paidOffDate, got %+v", final)
	}

	transactions, err := service.ListTransactions(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	paymentCount := 0
	for _, txn := range transactions {
		if txn.Type == loans.TransactionTypeInstallmentPayment {
			paymentCount++
		}
	}
	if paymentCount != 3 {
		t.Fatalf("expected 3 INSTALLMENT_PAYMENT transactions, got %d", paymentCount)
	}
}

// TestRepository_ApplyAmortization_RecalculatesRecurringCharges confirms an amortization against
// real Postgres both reduces the balance and rewrites future recurring-interest installments'
// interest/amount/balance columns.
func TestRepository_ApplyAmortization_RecalculatesRecurringCharges(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()

	clientID := seedClient(t, ctx, pool)
	repo := postgres.NewRepository(pool)
	service := loans.NewService(repo)

	contractDate := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	loan, err := service.Create(ctx, loans.CreateLoanRequest{
		ClientID: clientID, PrincipalAmount: "1000.00", MonthlyInterestRate: "5",
		PaymentType: loans.PaymentTypeRecurringInterest, ContractDate: civildate.Of(contractDate), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM loans WHERE id = $1`, loan.ID) //nolint:errcheck

	updated, err := service.ApplyAmortization(ctx, loan.ID, loans.ApplyAmortizationRequest{
		Amount: "400.00", PaymentDate: civildate.Of(time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("ApplyAmortization: %v", err)
	}
	if updated.OutstandingBalance != "600.00" {
		t.Fatalf("expected outstanding balance 600.00, got %s", updated.OutstandingBalance)
	}

	installments, err := service.ListInstallments(ctx, loan.ID)
	if err != nil {
		t.Fatalf("ListInstallments: %v", err)
	}
	if len(installments) == 0 {
		t.Fatal("expected at least one future recurring charge to recalculate")
	}
	for _, i := range installments {
		if i.InterestAmount != "30.00" || i.OutstandingBalance != "600.00" {
			t.Fatalf("expected recalculated charge interest=30.00 balance=600.00, got %+v", i)
		}
	}
}

// TestService_RolloverRecurringInterest_AgainstRealPostgres exercises Service.RolloverRecurringInterest
// against real Postgres: a RECURRING_INTEREST loan whose only installment is from a previous year
// gets extended, and calling it twice in a row for the same reference date is a no-op the second
// time — Hermes's stated acceptance criterion for this job (chat msg 91).
func TestService_RolloverRecurringInterest_AgainstRealPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()

	clientID := seedClient(t, ctx, pool)
	repo := postgres.NewRepository(pool)
	service := loans.NewService(repo)

	recurringLoan, err := service.Create(ctx, loans.CreateLoanRequest{
		ClientID: clientID, PrincipalAmount: "1000.00", MonthlyInterestRate: "5",
		PaymentType:  loans.PaymentTypeRecurringInterest,
		ContractDate: civildate.Of(time.Date(2025, time.November, 1, 0, 0, 0, 0, time.UTC)), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM loans WHERE id = $1`, recurringLoan.ID) //nolint:errcheck

	referenceDate := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)
	if err := service.RolloverRecurringInterest(ctx, referenceDate); err != nil {
		t.Fatalf("RolloverRecurringInterest (1st run): %v", err)
	}
	firstRun, err := service.ListInstallments(ctx, recurringLoan.ID)
	if err != nil {
		t.Fatalf("ListInstallments (1st run): %v", err)
	}
	if len(firstRun) <= 2 {
		t.Fatalf("expected the schedule to have grown past its original 2 installments (Nov+Dec 2025), got %d", len(firstRun))
	}

	if err := service.RolloverRecurringInterest(ctx, referenceDate); err != nil {
		t.Fatalf("RolloverRecurringInterest (2nd run, same reference date): %v", err)
	}
	secondRun, err := service.ListInstallments(ctx, recurringLoan.ID)
	if err != nil {
		t.Fatalf("ListInstallments (2nd run): %v", err)
	}
	if len(secondRun) != len(firstRun) {
		t.Fatalf("expected a repeat rollover to produce the identical installment set, got %d (was %d)", len(secondRun), len(firstRun))
	}
	for i := range firstRun {
		if firstRun[i] != secondRun[i] {
			t.Fatalf("expected installment %d unchanged after a repeat rollover, got %+v then %+v", i, firstRun[i], secondRun[i])
		}
	}
}

// fakeNotificationSender records notifications without touching the notifications table — the
// point of this integration test is loans' own overdue/due-soon SQL, not notifications
// persistence, which already has its own integration test.
type fakeNotificationSender struct {
	sent []notifications.CreateNotificationRequest
}

func (f *fakeNotificationSender) Create(_ context.Context, req notifications.CreateNotificationRequest) (notifications.NotificationDTO, error) {
	f.sent = append(f.sent, req)
	return notifications.NotificationDTO{ID: uuid.New(), Type: req.Type}, nil
}

// TestMaintenanceService_RunDailyMaintenance_AgainstRealPostgres exercises MaintenanceService's
// overdue-sweep and due-soon-alert steps against real Postgres.
func TestMaintenanceService_RunDailyMaintenance_AgainstRealPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()

	clientID := seedClient(t, ctx, pool)
	repo := postgres.NewRepository(pool)
	service := loans.NewService(repo)
	sender := &fakeNotificationSender{}
	maintenance := loans.NewMaintenanceService(repo, sender)

	// One installment due yesterday (must become OVERDUE) and one due in exactly dueSoonAlertDays
	// (must trigger a due-soon alert). Built via Create then backdated directly via SQL, since
	// Service.Create always dates installments relative to a real contract date, not "today".
	installmentLoan, err := service.Create(ctx, loans.CreateLoanRequest{
		ClientID: clientID, PrincipalAmount: "300.00", MonthlyInterestRate: "0",
		PaymentType: loans.PaymentTypeInstallment, InstallmentCount: 2, ContractDate: civildate.Of(time.Now()), DueDay: 1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM loans WHERE id = $1`, installmentLoan.ID) //nolint:errcheck

	installments, err := service.ListInstallments(ctx, installmentLoan.ID)
	if err != nil {
		t.Fatalf("ListInstallments: %v", err)
	}
	// Computed from civildate.Today() (São Paulo calendar day), not a naive time.Now(): the
	// maintenance service under test compares against civildate.Today().Time() too (see
	// maintenance.go), and a bare UTC time.Now().AddDate(...) drifts a calendar day off that
	// during the ~3h window where UTC has already rolled to the next day but São Paulo (UTC-3)
	// hasn't yet — flaky specifically around 00:00-03:00 UTC, not a real bug in the code under test.
	today := civildate.Today().Time()
	yesterday := today.AddDate(0, 0, -1)
	dueSoonDate := today.AddDate(0, 0, 3)
	if _, err := pool.Exec(ctx, `UPDATE installments SET due_date = $2 WHERE id = $1`, installments[0].ID, yesterday); err != nil {
		t.Fatalf("backdating installment 1: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE installments SET due_date = $2 WHERE id = $1`, installments[1].ID, dueSoonDate); err != nil {
		t.Fatalf("setting installment 2 due-soon: %v", err)
	}

	report, err := maintenance.RunDailyMaintenance(ctx)
	if err != nil {
		t.Fatalf("RunDailyMaintenance: %v", err)
	}
	if report.InstallmentsMarkedOverdue < 1 {
		t.Fatalf("expected at least 1 installment marked overdue, got %d", report.InstallmentsMarkedOverdue)
	}
	if report.DueSoonAlertsSent < 1 {
		t.Fatalf("expected at least 1 due-soon alert, got %d", report.DueSoonAlertsSent)
	}

	updated, err := service.ListInstallments(ctx, installmentLoan.ID)
	if err != nil {
		t.Fatalf("ListInstallments (after): %v", err)
	}
	if updated[0].Status != loans.InstallmentStatusOverdue {
		t.Fatalf("expected the backdated installment to be OVERDUE, got %v", updated[0].Status)
	}

	foundOverdueAlert, foundDueSoonAlert := false, false
	for _, n := range sender.sent {
		if n.Type == notifications.NotificationTypeInstallmentOverdue {
			foundOverdueAlert = true
		}
		if n.Type == notifications.NotificationTypeInstallmentDueSoon {
			foundDueSoonAlert = true
		}
	}
	if !foundOverdueAlert || !foundDueSoonAlert {
		t.Fatalf("expected both an overdue and a due-soon notification, got %+v", sender.sent)
	}
}
