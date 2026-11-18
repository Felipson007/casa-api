package loans

import (
	"testing"
	"time"
)

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// TestGenerateFixedSchedule_MatchesJavaDocExample hand-verifies the exact example from the old
// Java ParcelaCalculationService's class-level javadoc: "a 1000 loan at 10% in 3x is 333.33
// principal + 100 interest, three times, not an amortizing schedule" — the doc rounds informally;
// the real numbers (principal split 1000/3, last installment absorbing the rounding remainder)
// are 333.33/333.33/333.34, interest flat 100.00 every time.
func TestGenerateFixedSchedule_MatchesJavaDocExample(t *testing.T) {
	contractDate := date(2026, time.January, 15)
	schedule, err := GenerateFixedSchedule("1000.00", "10", 3, contractDate, 15, 1)
	if err != nil {
		t.Fatalf("GenerateFixedSchedule returned error: %v", err)
	}
	if len(schedule) != 3 {
		t.Fatalf("expected 3 installments, got %d", len(schedule))
	}

	want := []struct{ principal, interest, amount, balance string }{
		{"333.33", "100.00", "433.33", "666.67"},
		{"333.33", "100.00", "433.33", "333.34"},
		{"333.34", "100.00", "433.34", "0.00"},
	}
	for i, w := range want {
		got := schedule[i]
		if got.Principal != w.principal || got.Interest != w.interest || got.Amount != w.amount || got.OutstandingBalance != w.balance {
			t.Fatalf("installment %d: expected principal=%s interest=%s amount=%s balance=%s, got principal=%s interest=%s amount=%s balance=%s",
				i+1, w.principal, w.interest, w.amount, w.balance, got.Principal, got.Interest, got.Amount, got.OutstandingBalance)
		}
		if got.Number != i+1 {
			t.Fatalf("expected installment number %d, got %d", i+1, got.Number)
		}
	}

	// Due day == contract day-of-month here, so no proration — due dates are exactly 1/2/3 months
	// after the contract date.
	if !schedule[0].DueDate.Equal(date(2026, time.February, 15)) {
		t.Fatalf("expected first due date Feb 15 2026, got %v", schedule[0].DueDate)
	}
	if !schedule[2].DueDate.Equal(date(2026, time.April, 15)) {
		t.Fatalf("expected third due date Apr 15 2026, got %v", schedule[2].DueDate)
	}
}

// TestGenerateFixedSchedule_ProratesFirstInterestWhenDueDayPullsDateEarlier reproduces the exact
// bug scenario documented on ParcelaCalculationService.jurosProporcionalPrimeiraCobranca: a
// contract signed the 26th with dueDay=5 would otherwise owe a full month of interest for a
// ~10-day period.
func TestGenerateFixedSchedule_ProratesFirstInterestWhenDueDayPullsDateEarlier(t *testing.T) {
	contractDate := date(2026, time.January, 26)
	schedule, err := GenerateFixedSchedule("1000.00", "10", 2, contractDate, 5, 1)
	if err != nil {
		t.Fatalf("GenerateFixedSchedule returned error: %v", err)
	}

	firstDue := schedule[0].DueDate
	if !firstDue.Equal(date(2026, time.February, 5)) {
		t.Fatalf("expected first due date Feb 5 2026, got %v", firstDue)
	}
	// 10 days elapsed (Jan 26 -> Feb 5) out of a 31-day base period (Jan 26 -> Feb 26) —
	// 100.00 * 10/31 = 32.258064... -> 32.26 at 2 decimals.
	if schedule[0].Interest != "32.26" {
		t.Fatalf("expected prorated first interest 32.26, got %s", schedule[0].Interest)
	}
	// The second installment is a full, un-prorated month.
	if schedule[1].Interest != "100.00" {
		t.Fatalf("expected second installment's interest to be the full 100.00, got %s", schedule[1].Interest)
	}
}

func TestGenerateFixedSchedule_RejectsZeroOrNegativeCount(t *testing.T) {
	if _, err := GenerateFixedSchedule("1000.00", "10", 0, date(2026, time.January, 1), 1, 1); err == nil {
		t.Fatal("expected an error for installmentCount 0")
	}
}

// TestGenerateRecurringChargesThroughYearEnd_RunsThroughDecember confirms a UNICO loan's first
// batch covers every month from the first charge through December of that year, with a constant
// (non-amortizing) balance and Principal always "0.00".
func TestGenerateRecurringChargesThroughYearEnd_RunsThroughDecember(t *testing.T) {
	contractDate := date(2026, time.October, 10)
	charges, err := GenerateRecurringChargesThroughYearEnd("2000.00", "5", contractDate, 10, 1)
	if err != nil {
		t.Fatalf("GenerateRecurringChargesThroughYearEnd returned error: %v", err)
	}
	// First charge Nov 2026, then Dec 2026 — 2 charges to reach year-end.
	if len(charges) != 2 {
		t.Fatalf("expected 2 charges (Nov, Dec), got %d", len(charges))
	}
	for _, c := range charges {
		if c.Principal != "0.00" {
			t.Fatalf("expected Principal 0.00 for a recurring charge, got %s", c.Principal)
		}
		if c.OutstandingBalance != "2000.00" {
			t.Fatalf("expected the balance to stay at the original principal, got %s", c.OutstandingBalance)
		}
		// 2000 * 5% = 100.00 flat, no proration here since dueDay (10) matches contractDate's
		// own day-of-month (10).
		if c.Interest != "100.00" {
			t.Fatalf("expected flat interest 100.00, got %s", c.Interest)
		}
	}
	if !charges[0].DueDate.Equal(date(2026, time.November, 10)) {
		t.Fatalf("expected first charge Nov 10 2026, got %v", charges[0].DueDate)
	}
	if !charges[1].DueDate.Equal(date(2026, time.December, 10)) {
		t.Fatalf("expected second charge Dec 10 2026, got %v", charges[1].DueDate)
	}
}

func TestRecalculateRecurringCharge(t *testing.T) {
	interest, amount, balance, err := RecalculateRecurringCharge("1500.00", "5")
	if err != nil {
		t.Fatalf("RecalculateRecurringCharge returned error: %v", err)
	}
	if interest != "75.00" || amount != "75.00" || balance != "1500.00" {
		t.Fatalf("expected interest=amount=75.00, balance=1500.00, got interest=%s amount=%s balance=%s", interest, amount, balance)
	}
}

// TestDueDateForMonth_ClampsAcrossMonthLengths confirms month-end clamping matches Java's
// LocalDate.plusMonths + withDayOfMonth (Go's time.Time.AddDate does NOT clamp by default — this
// is exactly the bug schedule.go's targetMonth/dueDateForMonth avoid).
func TestDueDateForMonth_ClampsAcrossMonthLengths(t *testing.T) {
	contractDate := date(2026, time.January, 31)
	// 1 month after Jan 31 should clamp to Feb 28 (2026 is not a leap year), not overflow into March.
	got := dueDateForMonth(contractDate, 1, 31)
	if !got.Equal(date(2026, time.February, 28)) {
		t.Fatalf("expected Feb 28 2026 (clamped), got %v", got)
	}
}
