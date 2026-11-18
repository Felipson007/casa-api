package loans

import (
	"context"
	"fmt"
	"math/big"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/money"
)

// kpis.go ports DashboardService.calcularKpisEmLote exactly — same seven indicators the legacy
// history.xlsx spreadsheet's monthly "INDICADORES" row had, read from the real Java rather than
// guessed. Shares its population-per-month logic with vigentes.go's Vigentes (same underlying
// "which installments count for this month" rule — a QUITADO loan's installments only count in
// the exact month it was paid off), computed here for KPI aggregates instead of per-loan status.

// KPIsDTO mirrors DashboardKpisDto.
type KPIsDTO struct {
	CapitalAtRisk          string `json:"capitalAtRisk"`
	ExpectedThisMonth      string `json:"expectedThisMonth"`
	ReceivedThisMonth      string `json:"receivedThisMonth"`
	AmortizationsThisMonth string `json:"amortizationsThisMonth"`
	TotalThisMonth         string `json:"totalThisMonth"`
	ActiveThisMonth        int    `json:"activeThisMonth"`
	AverageRate            string `json:"averageRate"`
}

// MonthlyKPIsDTO is one month's KPIs + portfolio score, mirroring KpiMensalDto.
type MonthlyKPIsDTO struct {
	Year           int      `json:"year"`
	Month          int      `json:"month"`
	KPIs           *KPIsDTO `json:"kpis"`
	PortfolioScore *int     `json:"portfolioScore"`
}

// CalculateKPIs computes the seven KPIs for each of months — mirrors
// DashboardService.calcularKpisEmLote/calcularKpisDoMes exactly, including the QUITADO-only-in-
// its-payoff-month population filter (bucketizarParcelasPorMes) and reusing the same two
// payment-aggregation queries Vigentes uses (recebidoNoMes = installment payments bucketed by the
// installment's due month + one-off payments bucketed by their own date; amortizacoesNoMes =
// amortization transactions bucketed by their own date — the two types are told apart here, unlike
// vigentes.go's combined LoanAmountRow sum, since the KPI formula needs them separate).
func (s *Service) CalculateKPIs(ctx context.Context, months []YearMonth) (map[YearMonth]KPIsDTO, error) {
	if len(months) == 0 {
		return map[YearMonth]KPIsDTO{}, nil
	}
	minMonth, maxMonth := months[0], months[0]
	for _, m := range months[1:] {
		if m.firstOfMonth().Before(minMonth.firstOfMonth()) {
			minMonth = m
		}
		if m.firstOfMonth().After(maxMonth.firstOfMonth()) {
			maxMonth = m
		}
	}
	from := minMonth.firstOfMonth()
	toExclusive := maxMonth.AddMonths(1).firstOfMonth()

	relevant, err := s.repo.ListRelevantForVigentes(ctx)
	if err != nil {
		return nil, fmt.Errorf("loans: listing relevant loans for kpis: %w", err)
	}
	loanByID := make(map[uuid.UUID]LoanWithClientName, len(relevant))
	loanIDs := make([]uuid.UUID, len(relevant))
	for i, l := range relevant {
		loanByID[l.ID] = l
		loanIDs[i] = l.ID
	}

	installments, err := s.repo.ListInstallmentsDueInRange(ctx, loanIDs, from, toExclusive)
	if err != nil {
		return nil, fmt.Errorf("loans: listing installments for kpis: %w", err)
	}
	// populationByMonth[month] = every installment relevant that month — same QUITADO-only-in-
	// payoff-month filter as vigentesForMonth.
	populationByMonth := make(map[YearMonth][]Installment)
	for _, inst := range installments {
		loan, ok := loanByID[inst.LoanID]
		if !ok {
			continue
		}
		m := yearMonthOf(inst.DueDate)
		if loan.Status == StatusPaidOff {
			if loan.PaidOffDate == nil || yearMonthOf(*loan.PaidOffDate) != m {
				continue
			}
		}
		populationByMonth[m] = append(populationByMonth[m], inst)
	}

	installmentPayments, err := s.repo.ListInstallmentPaymentsByDueDate(ctx, from, toExclusive)
	if err != nil {
		return nil, fmt.Errorf("loans: listing installment payments for kpis: %w", err)
	}
	receivedByMonth := make(map[YearMonth]string)
	for _, row := range installmentPayments {
		sum, err := money.Add(orZeroMoney(receivedByMonth[row.Month]), row.Amount)
		if err != nil {
			return nil, err
		}
		receivedByMonth[row.Month] = sum
	}

	// otherPayments mixes ONE_OFF_PAYMENT (folds into recebidoNoMes) and AMORTIZATION (its own KPI
	// field, amortizacoesNoMes) — ListOtherPaymentsByPaymentDate doesn't distinguish them (vigentes
	// only needed the combined total), so this queries the transaction rows directly instead of
	// reusing that helper.
	oneOffByMonth, amortByMonth, err := s.repo.SumOneOffAndAmortizationByMonth(ctx, from, toExclusive)
	if err != nil {
		return nil, fmt.Errorf("loans: summing one-off/amortization payments for kpis: %w", err)
	}
	for m, amt := range oneOffByMonth {
		sum, err := money.Add(orZeroMoney(receivedByMonth[m]), amt)
		if err != nil {
			return nil, err
		}
		receivedByMonth[m] = sum
	}

	result := make(map[YearMonth]KPIsDTO, len(months))
	for _, m := range months {
		kpi, err := calculateMonthKPIs(populationByMonth[m], loanByID, orZeroMoney(receivedByMonth[m]), orZeroMoney(amortByMonth[m]))
		if err != nil {
			return nil, err
		}
		result[m] = kpi
	}
	return result, nil
}

// calculateMonthKPIs mirrors DashboardService.calcularKpisDoMes exactly: expectedThisMonth sums
// EVERY installment's amount (not deduped by loan); capitalAtRisk/averageRate are computed ONCE per
// unique loan in the population (a loan with 1 installment due this month is one loan, not counted
// twice) — PARCELADO loans contribute their fixed PrincipalAmount to capitalAtRisk, RECURRING_INTEREST
// loans contribute this specific installment's OutstandingBalance snapshot (the balance AS OF that
// month, not today's live balance).
func calculateMonthKPIs(population []Installment, loanByID map[uuid.UUID]LoanWithClientName, received, amortizations string) (KPIsDTO, error) {
	expected := money.Zero
	capitalAtRisk := money.Zero
	rateSum := new(big.Rat)
	counted := make(map[uuid.UUID]bool)

	for _, inst := range population {
		var err error
		expected, err = money.Add(expected, inst.InstallmentAmount)
		if err != nil {
			return KPIsDTO{}, err
		}
		loan, ok := loanByID[inst.LoanID]
		if !ok || counted[inst.LoanID] {
			continue
		}
		counted[inst.LoanID] = true

		var contribution string
		if loan.PaymentType == PaymentTypeInstallment {
			contribution = loan.PrincipalAmount
		} else {
			contribution = inst.OutstandingBalance
		}
		capitalAtRisk, err = money.Add(capitalAtRisk, contribution)
		if err != nil {
			return KPIsDTO{}, err
		}
		rate, err := money.Parse(loan.MonthlyInterestRate)
		if err != nil {
			return KPIsDTO{}, err
		}
		rateSum = new(big.Rat).Add(rateSum, rate)
	}

	averageRate := money.Zero
	if len(counted) > 0 {
		avg := new(big.Rat).Quo(rateSum, big.NewRat(int64(len(counted)), 1))
		averageRate = money.Format(money.Round2(avg))
	}
	total, err := money.Add(received, amortizations)
	if err != nil {
		return KPIsDTO{}, err
	}

	return KPIsDTO{
		CapitalAtRisk:          capitalAtRisk,
		ExpectedThisMonth:      expected,
		ReceivedThisMonth:      received,
		AmortizationsThisMonth: amortizations,
		TotalThisMonth:         total,
		ActiveThisMonth:        len(population),
		AverageRate:            averageRate,
	}, nil
}

func orZeroMoney(s string) string {
	if s == "" {
		return money.Zero
	}
	return s
}
