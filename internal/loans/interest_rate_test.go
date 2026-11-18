package loans

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

func TestService_ChangeInterestRate_RecomputesOnlyOpenInstallments(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	// RECURRING_INTEREST loan, principal 2000, 10% a month → each charge is 200.00.
	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "2000.00", MonthlyInterestRate: "10",
		PaymentType: PaymentTypeRecurringInterest, ContractDate: civildate.Of(date(2026, time.June, 1)), DueDay: 10,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before, _ := service.ListInstallments(ctx, loan.ID)
	if len(before) == 0 {
		t.Fatal("expected some installments")
	}
	// Pay the first one — it must be left EXACTLY as it was by the rate change (its amount can be
	// prorated for the first partial period, so capture it rather than assuming a flat value).
	paidAmountBefore := before[0].InstallmentAmount
	if _, err := service.MarkInstallmentPaid(ctx, loan.ID, before[0].ID, MarkInstallmentPaidRequest{}); err != nil {
		t.Fatalf("MarkInstallmentPaid: %v", err)
	}

	updated, err := service.ChangeInterestRate(ctx, loan.ID, "7.5")
	if err != nil {
		t.Fatalf("ChangeInterestRate: %v", err)
	}
	if updated.MonthlyInterestRate != "7.500000" {
		t.Fatalf("loan rate not updated: %q", updated.MonthlyInterestRate)
	}

	after, _ := service.ListInstallments(ctx, loan.ID)
	for _, inst := range after {
		switch inst.Status {
		case InstallmentStatusPaid:
			if inst.InstallmentAmount != paidAmountBefore {
				t.Errorf("paid installment #%d was rewritten: %s (want unchanged %s)", inst.InstallmentNumber, inst.InstallmentAmount, paidAmountBefore)
			}
		default:
			if inst.InstallmentAmount != "150.00" || inst.InterestAmount != "150.00" {
				t.Errorf("open installment #%d not re-rated: amount=%s interest=%s (want 150.00 — 2000 * 7.5%%)",
					inst.InstallmentNumber, inst.InstallmentAmount, inst.InterestAmount)
			}
		}
	}
}

func TestService_ChangeInterestRate_ZeroIsAllowed(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "1000.00", MonthlyInterestRate: "10",
		PaymentType: PaymentTypeRecurringInterest, ContractDate: civildate.Of(date(2026, time.June, 1)), DueDay: 10,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := service.ChangeInterestRate(ctx, loan.ID, "0"); err != nil {
		t.Fatalf("rate 0 should be allowed: %v", err)
	}
	after, _ := service.ListInstallments(ctx, loan.ID)
	for _, inst := range after {
		if inst.InstallmentAmount != "0.00" {
			t.Errorf("installment #%d should be 0.00 at rate 0, got %s", inst.InstallmentNumber, inst.InstallmentAmount)
		}
	}
}

func TestService_ChangeInterestRate_RejectsInstallmentLoans(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	loan, err := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "1000.00", MonthlyInterestRate: "2.5",
		PaymentType: PaymentTypeInstallment, InstallmentCount: 6,
		ContractDate: civildate.Of(date(2026, time.June, 1)), DueDay: 10,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := service.ChangeInterestRate(ctx, loan.ID, "1.5"); err == nil {
		t.Fatal("expected a rate change on a PARCELADO loan to be rejected")
	}
}

func TestService_ChangeInterestRate_RejectsNegative(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	loan, _ := service.Create(ctx, CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "1000.00", MonthlyInterestRate: "10",
		PaymentType: PaymentTypeRecurringInterest, ContractDate: civildate.Of(date(2026, time.June, 1)), DueDay: 10,
	})
	if _, err := service.ChangeInterestRate(ctx, loan.ID, "-1"); err == nil {
		t.Fatal("expected a negative rate to be rejected")
	}
}
