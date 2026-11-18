package goals

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func str(s string) *string { return &s }

// The scenario the module was built for: a R$ 530k house, entry funded by FGTS (120k) + a savings
// bucket (36k) already summed into FundedFromBalances by the repository, plus R$ 5k/month.
func TestCompute_HousePurchasePlan(t *testing.T) {
	td := day("2028-06-01")
	g := Goal{
		ID: uuid.New(), Name: "Casa", Kind: KindPurchase, Status: StatusActive,
		TargetAmount: "530000.00", CurrentAmount: "0", FundedFromBalances: "156000.00",
		MonthlyContribution: str("5000.00"), TargetDate: &td,
	}
	dto := (&Service{}).compute(g, day("2026-08-29"))

	if dto.Accumulated != "156000.00" {
		t.Fatalf("Accumulated = %q, want 156000.00", dto.Accumulated)
	}
	if dto.Gap != "374000.00" {
		t.Fatalf("Gap = %q, want 374000.00", dto.Gap)
	}
	if dto.ProgressPct != "29.4" {
		t.Fatalf("ProgressPct = %q, want 29.4", dto.ProgressPct)
	}
	if dto.MonthsToTarget == nil || *dto.MonthsToTarget != 75 { // ceil(374000 / 5000)
		t.Fatalf("MonthsToTarget = %v, want 75", dto.MonthsToTarget)
	}
	// 22 months to the deadline * 5000 = 110000 < 374000 gap.
	if dto.OnTrackForDate == nil || *dto.OnTrackForDate {
		t.Fatalf("OnTrackForDate = %v, want false", dto.OnTrackForDate)
	}
}

func TestCompute_AchievedGoalClampsGapAndMonths(t *testing.T) {
	g := Goal{
		ID: uuid.New(), Name: "Reserva", Kind: KindReserve, Status: StatusActive,
		TargetAmount: "10000.00", CurrentAmount: "12000.00", FundedFromBalances: "0",
	}
	dto := (&Service{}).compute(g, day("2026-08-29"))

	if dto.Gap != "0.00" {
		t.Fatalf("Gap = %q, want 0.00 (over-funded)", dto.Gap)
	}
	if dto.ProgressPct != "120.0" {
		t.Fatalf("ProgressPct = %q, want 120.0", dto.ProgressPct)
	}
	if dto.MonthsToTarget == nil || *dto.MonthsToTarget != 0 {
		t.Fatalf("MonthsToTarget = %v, want 0", dto.MonthsToTarget)
	}
	if dto.OnTrackForDate != nil {
		t.Fatalf("OnTrackForDate = %v, want nil (no target date / no contribution)", dto.OnTrackForDate)
	}
}

func TestValidateAmounts(t *testing.T) {
	s := &Service{}
	if err := s.validateAmounts("0", nil, nil); err == nil {
		t.Fatal("targetAmount 0 should be rejected")
	}
	if err := s.validateAmounts("100", str("-1"), nil); err == nil {
		t.Fatal("negative currentAmount should be rejected")
	}
	if err := s.validateAmounts("100", str("50"), str("10")); err != nil {
		t.Fatalf("valid amounts rejected: %v", err)
	}
}
