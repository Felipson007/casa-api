package loans

import (
	"errors"
	"math/big"
	"time"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// InstallmentCalc is a pure calculation result — one row of a generated schedule, not yet
// persisted. Kept separate from Installment so the math stays independently testable from
// persistence — mirrors the old Java ParcelaCalculationService/ParcelaCalculo split.
type InstallmentCalc struct {
	Number             int
	DueDate            time.Time
	Amount             string
	Principal          string
	Interest           string
	OutstandingBalance string
}

// GenerateFixedSchedule builds the fixed installment schedule for an INSTALLMENT (PARCELADO)
// loan: count installments, principal split evenly (rounded to 2 decimals; any leftover cent from
// rounding is absorbed entirely by the LAST installment, so the sum of every Principal always
// equals principal exactly), and a flat interest charge every installment computed once on the
// ORIGINAL principal — this does NOT decline between installments (verified against the real
// business model: a 1000 loan at 10% in 3x is 333.33/333.33/333.34 principal + 100.00 interest
// flat, not an amortizing schedule). The first installment's interest is prorated by actual days
// elapsed when monthsToFirst is 1 and dueDay pulls the first due date away from contractDate's own
// day-of-month — see prorateFirstInterest.
//
// monthsToFirst is how many months after contractDate the first installment falls: 1 for the
// normal case, 2 to skip the near month (mirrors CreateEmprestimoRequest.pularPrimeiroMes).
func GenerateFixedSchedule(principalStr, monthlyRatePercentStr string, count int, contractDate time.Time, dueDay int, monthsToFirst int) ([]InstallmentCalc, error) {
	if count < 1 {
		return nil, apperr.Validation("installmentCount is required and must be >= 1 for INSTALLMENT loans", "installmentCount")
	}
	principal, err := parseMoney(principalStr)
	if err != nil {
		return nil, apperr.Validation("principalAmount must be a valid decimal", "principalAmount")
	}
	rate, err := parseMoney(monthlyRatePercentStr)
	if err != nil {
		return nil, apperr.Validation("monthlyInterestRate must be a valid decimal", "monthlyInterestRate")
	}

	principalBase := roundTo2(new(big.Rat).Quo(principal, big.NewRat(int64(count), 1)))
	fullInterest := roundTo2(new(big.Rat).Quo(new(big.Rat).Mul(principal, rate), big.NewRat(100, 1)))

	result := make([]InstallmentCalc, 0, count)
	accumulated := new(big.Rat)
	for n := 1; n <= count; n++ {
		principalPortion := principalBase
		if n == count {
			principalPortion = new(big.Rat).Sub(principal, accumulated)
		}
		accumulated = new(big.Rat).Add(accumulated, principalPortion)
		outstandingAfter := roundTo2(new(big.Rat).Sub(principal, accumulated))

		dueDate := dueDateForMonth(contractDate, n-1+monthsToFirst, dueDay)
		interestPortion := fullInterest
		if n == 1 && monthsToFirst == 1 {
			interestPortion = prorateFirstInterest(fullInterest, contractDate, dueDate)
		}
		amount := roundTo2(new(big.Rat).Add(principalPortion, interestPortion))

		result = append(result, InstallmentCalc{
			Number: n, DueDate: dueDate,
			Amount: money2(amount), Principal: money2(principalPortion), Interest: money2(interestPortion),
			OutstandingBalance: money2(outstandingAfter),
		})
	}
	return result, nil
}

// GenerateRecurringChargesThroughYearEnd builds a RECURRING_INTEREST (UNICO) loan's first batch of
// monthly interest charges: starting monthsToFirst months after contractDate, one per calendar
// month through December of that target year — mirrors
// ParcelaCalculationService.calcularRecorrenteAteFimDoAno's first-generation case. Principal never
// amortizes here (Principal is always "0.00", OutstandingBalance always equals principal) —
// matches the loan's real state before any payment/amortization is made.
//
// This covers the first-batch generation done at loan creation (Service.Create). The continuation
// case — extending an already-scheduled loan into a new year — is ContinueRecurringSchedule,
// below, used by MaintenanceService's daily rollover (maintenance.go).
func GenerateRecurringChargesThroughYearEnd(principalStr, monthlyRatePercentStr string, contractDate time.Time, dueDay int, monthsToFirst int) ([]InstallmentCalc, error) {
	principal, err := parseMoney(principalStr)
	if err != nil {
		return nil, apperr.Validation("principalAmount must be a valid decimal", "principalAmount")
	}
	rate, err := parseMoney(monthlyRatePercentStr)
	if err != nil {
		return nil, apperr.Validation("monthlyInterestRate must be a valid decimal", "monthlyInterestRate")
	}

	fullInterest := roundTo2(new(big.Rat).Quo(new(big.Rat).Mul(principal, rate), big.NewRat(100, 1)))
	balance := roundTo2(principal)

	var result []InstallmentCalc
	cursor := targetMonth(contractDate, monthsToFirst)
	targetYear := cursor.Year()
	number := 1
	for cursor.Year() == targetYear {
		last := lastDayOfMonth(cursor)
		day := dueDay
		if day > last {
			day = last
		}
		dueDate := time.Date(cursor.Year(), cursor.Month(), day, 0, 0, 0, 0, time.UTC)

		interest := fullInterest
		if number == 1 && monthsToFirst == 1 {
			interest = prorateFirstInterest(fullInterest, contractDate, dueDate)
		}
		result = append(result, InstallmentCalc{
			Number: number, DueDate: dueDate,
			Amount: money2(interest), Principal: "0.00", Interest: money2(interest),
			OutstandingBalance: money2(balance),
		})
		number++
		cursor = targetMonth(cursor, 1)
	}
	return result, nil
}

// ContinueRecurringSchedule extends an already-scheduled RECURRING_INTEREST loan from just after
// its last installment through December of that installment's next year — mirrors the
// continuation branch of EmprestimoRecorrenteService.garantirParcelasAteFimDoAno (as opposed to
// GenerateRecurringChargesThroughYearEnd's first-batch case at loan creation). Used by the daily
// rollover (MaintenanceService, maintenance.go). No first-charge proration here — that only
// applies to a brand-new contract's very first charge.
func ContinueRecurringSchedule(lastInstallmentNumber int, lastDueDate time.Time, currentBalanceStr, monthlyRatePercentStr string, dueDay int) ([]InstallmentCalc, error) {
	balance, err := parseMoney(currentBalanceStr)
	if err != nil {
		return nil, apperr.Validation("outstandingBalance must be a valid decimal")
	}
	rate, err := parseMoney(monthlyRatePercentStr)
	if err != nil {
		return nil, apperr.Validation("monthlyInterestRate must be a valid decimal")
	}

	fullInterest := roundTo2(new(big.Rat).Quo(new(big.Rat).Mul(balance, rate), big.NewRat(100, 1)))
	balanceRounded := roundTo2(balance)

	var result []InstallmentCalc
	cursor := targetMonth(lastDueDate, 1)
	targetYear := cursor.Year()
	number := lastInstallmentNumber + 1
	for cursor.Year() == targetYear {
		last := lastDayOfMonth(cursor)
		day := dueDay
		if day > last {
			day = last
		}
		dueDate := time.Date(cursor.Year(), cursor.Month(), day, 0, 0, 0, 0, time.UTC)
		result = append(result, InstallmentCalc{
			Number: number, DueDate: dueDate,
			Amount: money2(fullInterest), Principal: "0.00", Interest: money2(fullInterest),
			OutstandingBalance: money2(balanceRounded),
		})
		number++
		cursor = targetMonth(cursor, 1)
	}
	return result, nil
}

// RecalculateRecurringCharge recomputes one RECURRING_INTEREST installment's Interest/Amount after
// an amortization changes the loan's balance — mirrors
// EmprestimoRecorrenteService.aplicarAmortizacao's per-installment recalculation. Principal stays
// "0.00" (amortization is a separate, explicit action from installment payment for this loan
// type).
func RecalculateRecurringCharge(newBalanceStr, monthlyRatePercentStr string) (interest, amount, outstandingBalance string, err error) {
	balance, err := parseMoney(newBalanceStr)
	if err != nil {
		return "", "", "", apperr.Validation("outstandingBalance must be a valid decimal", "outstandingBalance")
	}
	rate, err := parseMoney(monthlyRatePercentStr)
	if err != nil {
		return "", "", "", apperr.Validation("monthlyInterestRate must be a valid decimal", "monthlyInterestRate")
	}
	newInterest := roundTo2(new(big.Rat).Quo(new(big.Rat).Mul(balance, rate), big.NewRat(100, 1)))
	return money2(newInterest), money2(newInterest), money2(roundTo2(balance)), nil
}

// reduceBalance mirrors Emprestimo.reduzirSaldoDevedor: applies min(amount, currentBalance) to
// currentBalance (never negative), and reports StatusPaidOff with paidOffDate set when that
// zeroes the balance. The two things (status transition + paid-off date) always happen together,
// matching the Java invariant the same method's javadoc calls out (violated once in production
// before this helper existed there).
func reduceBalance(currentBalanceStr, amountStr string, eventDate time.Time) (newBalance string, newStatus Status, paidOffDate *time.Time, err error) {
	balance, err := parseMoney(currentBalanceStr)
	if err != nil {
		return "", "", nil, apperr.Validation("outstandingBalance must be a valid decimal")
	}
	amount, err := parseMoney(amountStr)
	if err != nil {
		return "", "", nil, apperr.Validation("amount must be a valid decimal", "amount")
	}

	applied := amount
	if amount.Cmp(balance) > 0 {
		applied = balance
	}
	remaining := roundTo2(new(big.Rat).Sub(balance, applied))
	if remaining.Sign() < 0 {
		remaining = new(big.Rat)
	}

	if remaining.Sign() == 0 {
		d := eventDate
		return money2(remaining), StatusPaidOff, &d, nil
	}
	return money2(remaining), StatusActive, nil, nil
}

// parseMoney parses a decimal string into an exact rational — math/big is stdlib, so this avoids
// float rounding without adding a decimal-library dependency (docs/architecture-plan.md section 3,
// same reasoning as internal/finance's amountsDiffer).
func parseMoney(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, errors.New("not a valid decimal")
	}
	return r, nil
}

// roundTo2 rounds r to 2 decimal places, half-away-from-zero (equivalent to Java's
// RoundingMode.HALF_UP for the non-negative amounts this module only ever deals with) — see
// (*big.Rat).FloatString's documented rounding behavior. Round-tripping through FloatString gives
// an exact multiple of 1/100 back as a Rat, so further arithmetic (accumulation, subtraction) stays
// exact rather than compounding float error.
func roundTo2(r *big.Rat) *big.Rat {
	rounded, _ := new(big.Rat).SetString(r.FloatString(2))
	return rounded
}

func money2(r *big.Rat) string {
	return r.FloatString(2)
}

// prorateFirstInterest scales a flat monthly interest charge by how many days actually elapsed
// until the first due date, relative to a normal one-month cycle from contractDate — mirrors
// ParcelaCalculationService.jurosProporcionalPrimeiraCobranca exactly, including its rationale:
// without this, a borrower whose dueDay falls earlier in the month than contractDate (e.g.
// contract signed the 26th, due day set to the 5th) would owe a FULL month of interest for a
// period as short as ~10 days.
func prorateFirstInterest(fullInterest *big.Rat, contractDate, firstDueDate time.Time) *big.Rat {
	baseEnd := plusMonthsClamped(contractDate, 1)
	daysBase := daysBetween(contractDate, baseEnd)
	if daysBase <= 0 {
		return fullInterest
	}
	daysInPeriod := daysBetween(contractDate, firstDueDate)
	ratio := roundToN(new(big.Rat).Quo(big.NewRat(daysInPeriod, 1), big.NewRat(daysBase, 1)), 6)
	return roundTo2(new(big.Rat).Mul(fullInterest, ratio))
}

func roundToN(r *big.Rat, decimals int) *big.Rat {
	rounded, _ := new(big.Rat).SetString(r.FloatString(decimals))
	return rounded
}

func daysBetween(a, b time.Time) int64 {
	return int64(b.Sub(a).Hours() / 24)
}

// targetMonth returns the 1st of the month `offsetMonths` after contractDate's month — Go's
// time.Time.AddDate does NOT clamp day-of-month on month-length overflow (Jan 31 + 1 month becomes
// March 2/3, not Feb 28, unlike Java's LocalDate.plusMonths) — this computes year/month arithmetic
// directly instead, deliberately avoiding AddDate for month steps.
func targetMonth(t time.Time, offsetMonths int) time.Time {
	totalMonths := int(t.Month()) - 1 + offsetMonths
	year := t.Year() + totalMonths/12
	month := totalMonths % 12
	if month < 0 {
		month += 12
		year--
	}
	return time.Date(year, time.Month(month+1), 1, 0, 0, 0, 0, time.UTC)
}

// lastDayOfMonth returns how many days are in monthStart's month. monthStart must be the 1st of
// its month (as targetMonth always returns) so AddDate(0,1,0) can't hit the overflow problem
// described on targetMonth.
func lastDayOfMonth(monthStart time.Time) int {
	nextMonth := monthStart.AddDate(0, 1, 0)
	return nextMonth.AddDate(0, 0, -1).Day()
}

// dueDateForMonth returns dueDay within the month offsetMonths after contractDate's month, clamped
// down to that month's last day if dueDay overflows it — mirrors
// ParcelaCalculationService.vencimentoDoMes.
func dueDateForMonth(contractDate time.Time, offsetMonths, dueDay int) time.Time {
	monthStart := targetMonth(contractDate, offsetMonths)
	last := lastDayOfMonth(monthStart)
	day := dueDay
	if day > last {
		day = last
	}
	return time.Date(monthStart.Year(), monthStart.Month(), day, 0, 0, 0, 0, time.UTC)
}

// plusMonthsClamped mirrors Java's LocalDate.plusMonths: advances by `months`, keeping t's own
// day-of-month, clamped down if the target month is shorter (e.g. Jan 31 + 1 month = Feb 28).
func plusMonthsClamped(t time.Time, months int) time.Time {
	monthStart := targetMonth(t, months)
	last := lastDayOfMonth(monthStart)
	day := t.Day()
	if day > last {
		day = last
	}
	return time.Date(monthStart.Year(), monthStart.Month(), day, 0, 0, 0, 0, time.UTC)
}
