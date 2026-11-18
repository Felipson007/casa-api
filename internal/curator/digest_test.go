package curator

import (
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/finance"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

func strptr(s string) *string { return &s }

func activeLoan(client, principal, rate string) loans.LoanDTO {
	return loans.LoanDTO{
		ID: uuid.New(), ClientID: uuid.MustParse(client),
		PrincipalAmount: principal, MonthlyInterestRate: rate,
		OutstandingBalance: principal, Status: loans.StatusActive,
	}
}

// Mirrors the real production book that the model miscounted ("24 active loans totalling
// R$ 45.200" — actual: 22 and R$ 39.400).
func TestLoansDigest_CountsAndSumsExactly(t *testing.T) {
	maicon := uuid.NewString()
	other := uuid.NewString()
	names := map[string]string{maicon: "Maicon Mernick", other: "Fulano"}

	var book []loans.LoanDTO
	for i := 0; i < 10; i++ {
		book = append(book, activeLoan(other, "1220.00", "15.000000")) // 10 @ 15% = 12.200
	}
	for _, p := range []string{"2000.00", "1000.00", "1000.00", "1200.00", "500.00", "2000.00", "2000.00", "2000.00", "2000.00"} {
		book = append(book, activeLoan(maicon, p, "7.500000")) // 9 @ 7,5% = 13.700, all Maicon
	}
	book = append(book, activeLoan(other, "3000.00", "12.000000"))
	book = append(book, activeLoan(other, "3000.00", "13.330000"))
	book = append(book, activeLoan(other, "7500.00", "16.200000"))
	for i := 0; i < 3; i++ {
		l := activeLoan(other, "1000.00", "10.000000")
		l.Status = loans.StatusPaidOff
		l.OutstandingBalance = "0.00"
		book = append(book, l)
	}
	c := activeLoan(other, "1000.00", "20.000000")
	c.Status = loans.StatusCanceled
	book = append(book, c)

	d := loansDigestOf(book, names)

	if d.ActiveCount != 22 {
		t.Fatalf("ActiveCount = %d, want 22", d.ActiveCount)
	}
	if d.ActivePrincipalTotal != "39400.00" {
		t.Fatalf("ActivePrincipalTotal = %q, want 39400.00", d.ActivePrincipalTotal)
	}
	if d.ActiveOutstandingTotal != "39400.00" {
		t.Fatalf("ActiveOutstandingTotal = %q, want 39400.00", d.ActiveOutstandingTotal)
	}
	// (15*12200 + 7.5*13700 + 12*3000 + 13.33*3000 + 16.2*7500) / 39400 = 12.26...
	if d.ActiveWeightedMonthlyRatePct != "12.26" {
		t.Fatalf("ActiveWeightedMonthlyRatePct = %q, want 12.26", d.ActiveWeightedMonthlyRatePct)
	}

	var active *statusBucket
	for i := range d.ByStatus {
		if d.ByStatus[i].Status == "ACTIVE" {
			active = &d.ByStatus[i]
		}
	}
	if active == nil || active.Count != 22 {
		t.Fatalf("ByStatus ACTIVE = %+v, want count 22", active)
	}

	// Rate buckets sorted by count desc: 15% (10) then 7,5% (9).
	if len(d.ActiveByMonthlyRate) != 5 {
		t.Fatalf("got %d rate buckets, want 5", len(d.ActiveByMonthlyRate))
	}
	if d.ActiveByMonthlyRate[0].MonthlyRatePct != "15.00" || d.ActiveByMonthlyRate[0].Count != 10 {
		t.Fatalf("top rate bucket = %+v, want 15.00 x10", d.ActiveByMonthlyRate[0])
	}
	if d.ActiveByMonthlyRate[1].MonthlyRatePct != "7.50" || d.ActiveByMonthlyRate[1].Count != 9 {
		t.Fatalf("2nd rate bucket = %+v, want 7.50 x9", d.ActiveByMonthlyRate[1])
	}
	if len(d.ActiveByMonthlyRate[1].Borrowers) != 1 || d.ActiveByMonthlyRate[1].Borrowers[0] != "Maicon Mernick" {
		t.Fatalf("7,5%% bucket borrowers = %v, want [Maicon Mernick]", d.ActiveByMonthlyRate[1].Borrowers)
	}

	// Borrower rollup: Maicon has all 9 of the cheapest loans.
	var maiconRow *borrowerRow
	for i := range d.ActiveByBorrower {
		if d.ActiveByBorrower[i].Name == "Maicon Mernick" {
			maiconRow = &d.ActiveByBorrower[i]
		}
	}
	if maiconRow == nil || maiconRow.ActiveLoans != 9 || maiconRow.PrincipalTotal != "13700.00" {
		t.Fatalf("Maicon row = %+v, want 9 loans / 13700.00", maiconRow)
	}
	if len(maiconRow.MonthlyRatesPct) != 1 || maiconRow.MonthlyRatesPct[0] != "7.50" {
		t.Fatalf("Maicon rates = %v, want [7.50]", maiconRow.MonthlyRatesPct)
	}
}

func TestSpendingDigest_TopMerchantsAndDelta(t *testing.T) {
	in := digestInput{
		expCurrent:  expenses.MonthlySummaryDTO{TotalAmount: "1500.00", EntryCount: 12},
		expPrevious: expenses.MonthlySummaryDTO{TotalAmount: "1200.00", EntryCount: 10},
		expYTD:      expenses.AnnualSummaryDTO{TotalAmount: "9000.00", EntryCount: 80},
		expenseItems: []expenses.ExpenseDTO{
			{ID: uuid.New(), Amount: "20.00", Description: strptr("Uber")},
			{ID: uuid.New(), Amount: "35.00", Description: strptr("Uber")},
			{ID: uuid.New(), Amount: "15.00", Description: strptr("uber ")},
			{ID: uuid.New(), Amount: "200.00", Description: strptr("Aluguel")},
			{ID: uuid.New(), Amount: "10.00", Description: nil},
		},
	}
	d := spendingDigestOf(in)

	if d.MonthOverMonthDelta != "300.00" {
		t.Fatalf("MonthOverMonthDelta = %q, want 300.00", d.MonthOverMonthDelta)
	}
	if len(d.TopMerchantsYTD) != 2 {
		t.Fatalf("got %d merchants, want 2", len(d.TopMerchantsYTD))
	}
	// Aluguel (200) ranks above Uber (70 across 3 charges, case/space-insensitive).
	if d.TopMerchantsYTD[0].Description != "Aluguel" || d.TopMerchantsYTD[0].Total != "200.00" {
		t.Fatalf("top merchant = %+v, want Aluguel/200.00", d.TopMerchantsYTD[0])
	}
	if d.TopMerchantsYTD[1].Total != "70.00" || d.TopMerchantsYTD[1].Count != 3 {
		t.Fatalf("2nd merchant = %+v, want 70.00 x3 (Uber)", d.TopMerchantsYTD[1])
	}
}

func TestIncomeDigest_OmitsForeignSalaryWhenAbsent(t *testing.T) {
	in := digestInput{incCurrent: income.MonthlySummaryDTO{NetTotal: "5000.00"}}
	d := incomeDigestOf(in)
	if d.ForeignSalary != nil {
		t.Fatalf("expected ForeignSalary to be nil for a month with no USD income, got %+v", d.ForeignSalary)
	}
}

func TestIncomeDigest_IncludesForeignSalaryWhenPresent(t *testing.T) {
	in := digestInput{incCurrent: income.MonthlySummaryDTO{
		NetTotal: "10200.00", USDTotal: "2000.00", ConvertedBRLTotal: "10200.00", AvgExchangeRate: "5.1000",
	}}
	d := incomeDigestOf(in)
	if d.ForeignSalary == nil {
		t.Fatal("expected ForeignSalary to be populated")
	}
	if d.ForeignSalary.USDReceived != "2000.00" || d.ForeignSalary.BRLTotal != "10200.00" || d.ForeignSalary.EffectiveRateBRL != "5.1000" {
		t.Fatalf("ForeignSalary = %+v, want {2000.00 10200.00 5.1000}", d.ForeignSalary)
	}
}

func TestBalancesDigest_GrandTotalByTypeAndStalest(t *testing.T) {
	old := civildate.Date{}
	_ = old.UnmarshalJSON([]byte(`"2025-01-10"`))
	recent := civildate.Date{}
	_ = recent.UnmarshalJSON([]byte(`"2026-08-01"`))

	d := balancesDigestOf([]finance.BalanceDTO{
		{ID: uuid.New(), Name: "FGTS", Type: finance.BalanceTypeFGTS, CurrentValue: "120000.00", UpdatedOn: old},
		{ID: uuid.New(), Name: "Caixinha Itaú", Type: finance.BalanceTypeSavings, CurrentValue: "36000.00", UpdatedOn: recent},
	})

	if d.GrandTotal != "156000.00" {
		t.Fatalf("GrandTotal = %q, want 156000.00", d.GrandTotal)
	}
	if len(d.ByType) != 2 || d.ByType[0].Type != "FGTS" || d.ByType[0].Total != "120000.00" {
		t.Fatalf("ByType = %+v, want FGTS first at 120000.00", d.ByType)
	}
	if d.Stalest == nil || d.Stalest.Name != "FGTS" {
		t.Fatalf("Stalest = %+v, want FGTS (oldest UpdatedOn)", d.Stalest)
	}
}
