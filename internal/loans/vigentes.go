package loans

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// addMoney sums two numeric(15,2)-shaped strings, reusing schedule.go's parseMoney/money2 —
// same "string in, string out" idiom the rest of this package uses for money to avoid float
// rounding.
func addMoney(a, b string) (string, error) {
	ra, err := parseMoney(a)
	if err != nil {
		return "", err
	}
	rb, err := parseMoney(b)
	if err != nil {
		return "", err
	}
	return money2(roundTo2(new(big.Rat).Add(ra, rb))), nil
}

// Part 3 (this pass) adds what the domain.go doc comment (part 2) explicitly deferred: the
// balance-ranking "vigentes" monthly dashboard aggregation — mirrors the real
// EmprestimoVigentesService.listarVigentesEmLote/listarVigentesDoMes exactly, read from the Java
// source rather than guessed. Still deferred (unrelated to this specific gap): full-replacement
// Update, due-day changes, Cancel — see loans_lifecycle.go, added alongside this in the same pass.

// YearMonth is a plain (year, month) pair — Go has no built-in equivalent to java.time.YearMonth.
type YearMonth struct {
	Year  int
	Month int // 1-12
}

func yearMonthOf(t time.Time) YearMonth { return YearMonth{Year: t.Year(), Month: int(t.Month())} }

// firstOfMonth returns the first-of-month UTC instant for (year, month) — used both as a map key
// helper and as the inclusive start of a query range.
func (ym YearMonth) firstOfMonth() time.Time {
	return time.Date(ym.Year, time.Month(ym.Month), 1, 0, 0, 0, 0, time.UTC)
}

// AddMonths returns the YearMonth n months after ym (n may be negative).
func (ym YearMonth) AddMonths(n int) YearMonth {
	t := ym.firstOfMonth().AddDate(0, n, 0)
	return yearMonthOf(t)
}

// MonthStatus mirrors SituacaoEmprestimoMes — a loan's standing for one specific calendar month.
type MonthStatus string

const (
	MonthStatusPaid    MonthStatus = "PAID"
	MonthStatusPending MonthStatus = "PENDING"
	MonthStatusOverdue MonthStatus = "OVERDUE"
	MonthStatusPaidOff MonthStatus = "PAID_OFF"
)

// LoanSummaryDTO mirrors EmprestimoResumoDto — one loan's standing for one calendar month.
// InstallmentDueDate is civildate.Date, not time.Time — see civildate's doc comment: a plain
// time.Time here marshaled as a full UTC instant, which a negative-UTC-offset client (Brazil)
// reads back as one day earlier once formatted in local time. This is the dashboard's "vencimento
// no mês" field reported showing every date one day short.
type LoanSummaryDTO struct {
	LoanID              uuid.UUID       `json:"loanId"`
	ClientID            uuid.UUID       `json:"clientId"`
	ClientName          string          `json:"clientName"`
	PrincipalAmount     string          `json:"principalAmount"`
	OutstandingBalance  string          `json:"outstandingBalance"` // snapshotted AT that month, not "live" today
	MonthlyInterestRate string          `json:"monthlyInterestRate"`
	PaymentType         PaymentType     `json:"paymentType"`
	InstallmentDueDate  *civildate.Date `json:"installmentDueDate,omitempty"`
	InterestThisMonth   string          `json:"interestThisMonth"`
	AmountPaidThisMonth string          `json:"amountPaidThisMonth"`
	Status              MonthStatus     `json:"status"`
}

// LoanWithClientName is a Loan plus its client's name pre-resolved (avoids N+1 in the aggregation).
type LoanWithClientName struct {
	Loan
	ClientName string
}

// LoanAmountRow is one (loan, month, amount) bucket — the shape both payment-aggregation queries
// return, pre-grouped by the repository's own GROUP BY rather than summed row-by-row in Go.
type LoanAmountRow struct {
	LoanID uuid.UUID
	Month  YearMonth
	Amount string
}

// Vigentes computes every relevant loan's standing for each of months — mirrors
// EmprestimoVigentesService.listarVigentesEmLote/listarVigentesDoMes. O(1) round trips regardless
// of len(months) (3 bulk queries total, bucketed in Go), same discipline as
// DashboardService.calcularKpisEmLote.
func (s *Service) Vigentes(ctx context.Context, months []YearMonth) (map[YearMonth][]LoanSummaryDTO, error) {
	vr := s.repo
	if len(months) == 0 {
		return map[YearMonth][]LoanSummaryDTO{}, nil
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

	relevant, err := vr.ListRelevantForVigentes(ctx)
	if err != nil {
		return nil, fmt.Errorf("loans: listing relevant loans for vigentes: %w", err)
	}
	loanIDs := make([]uuid.UUID, len(relevant))
	for i, l := range relevant {
		loanIDs[i] = l.ID
	}

	installments, err := vr.ListInstallmentsDueInRange(ctx, loanIDs, from, toExclusive)
	if err != nil {
		return nil, fmt.Errorf("loans: listing installments for vigentes: %w", err)
	}
	// installmentByLoanAndMonth[loanID][month] — at most one installment per loan per month in
	// practice (matches the Java Map<UUID, Map<YearMonth, Parcela>>).
	installmentByLoanAndMonth := make(map[uuid.UUID]map[YearMonth]Installment)
	for _, inst := range installments {
		m := yearMonthOf(inst.DueDate)
		if installmentByLoanAndMonth[inst.LoanID] == nil {
			installmentByLoanAndMonth[inst.LoanID] = make(map[YearMonth]Installment)
		}
		installmentByLoanAndMonth[inst.LoanID][m] = inst
	}

	paidByLoanAndMonth := make(map[uuid.UUID]map[YearMonth]string)
	addPaid := func(rows []LoanAmountRow) error {
		for _, row := range rows {
			if paidByLoanAndMonth[row.LoanID] == nil {
				paidByLoanAndMonth[row.LoanID] = make(map[YearMonth]string)
			}
			existing := paidByLoanAndMonth[row.LoanID][row.Month]
			if existing == "" {
				existing = "0"
			}
			sum, err := addMoney(existing, row.Amount)
			if err != nil {
				return err
			}
			paidByLoanAndMonth[row.LoanID][row.Month] = sum
		}
		return nil
	}
	installmentPayments, err := vr.ListInstallmentPaymentsByDueDate(ctx, from, toExclusive)
	if err != nil {
		return nil, fmt.Errorf("loans: listing installment payments for vigentes: %w", err)
	}
	if err := addPaid(installmentPayments); err != nil {
		return nil, err
	}
	otherPayments, err := vr.ListOtherPaymentsByPaymentDate(ctx, from, toExclusive)
	if err != nil {
		return nil, fmt.Errorf("loans: listing other payments for vigentes: %w", err)
	}
	if err := addPaid(otherPayments); err != nil {
		return nil, err
	}

	// civildate.Today(), not time.Now() — see its doc comment: a bare time.Now() has two bugs at
	// once here, a UTC-vs-America/Sao_Paulo day-rollover skew and a midnight-DueDate-vs-
	// time-of-day-today comparison that makes a same-day installment register as overdue from
	// 00:00:01 onward instead of only starting the day after it's due.
	today := civildate.Today().Time()
	result := make(map[YearMonth][]LoanSummaryDTO, len(months))
	for _, m := range months {
		result[m] = vigentesForMonth(m, relevant, installmentByLoanAndMonth, paidByLoanAndMonth, today)
	}
	return result, nil
}

func vigentesForMonth(
	month YearMonth,
	relevant []LoanWithClientName,
	installmentByLoanAndMonth map[uuid.UUID]map[YearMonth]Installment,
	paidByLoanAndMonth map[uuid.UUID]map[YearMonth]string,
	today time.Time,
) []LoanSummaryDTO {
	var result []LoanSummaryDTO
	for _, loan := range relevant {
		paid := paidByLoanAndMonth[loan.ID][month]
		if paid == "" {
			paid = "0.00"
		}

		if loan.Status == StatusPaidOff {
			// PAID_OFF only ever appears in the month it actually happened — no PaidOffDate (legacy
			// data) means we can't know which month that was, so it's simply omitted.
			if loan.PaidOffDate == nil || yearMonthOf(*loan.PaidOffDate) != month {
				continue
			}
			inst, hasInst := installmentByLoanAndMonth[loan.ID][month]
			var dueDate *civildate.Date
			interest := "0.00"
			balance := "0.00"
			if hasInst {
				dueDate = civildate.OfPtr(&inst.DueDate)
				interest = inst.InterestAmount
				balance = inst.OutstandingBalance
			}
			result = append(result, LoanSummaryDTO{
				LoanID: loan.ID, ClientID: loan.ClientID, ClientName: loan.ClientName,
				PrincipalAmount: loan.PrincipalAmount, OutstandingBalance: balance,
				MonthlyInterestRate: loan.MonthlyInterestRate, PaymentType: loan.PaymentType,
				InstallmentDueDate: dueDate, InterestThisMonth: interest, AmountPaidThisMonth: paid,
				Status: MonthStatusPaidOff,
			})
			continue
		}

		inst, ok := installmentByLoanAndMonth[loan.ID][month]
		if !ok {
			continue
		}
		var status MonthStatus
		switch {
		case inst.Status == InstallmentStatusPaid:
			status = MonthStatusPaid
		case inst.Status == InstallmentStatusOverdue || inst.DueDate.Before(today):
			status = MonthStatusOverdue
		default:
			status = MonthStatusPending
		}
		dueDate := civildate.Of(inst.DueDate)
		result = append(result, LoanSummaryDTO{
			LoanID: loan.ID, ClientID: loan.ClientID, ClientName: loan.ClientName,
			PrincipalAmount: loan.PrincipalAmount, OutstandingBalance: inst.OutstandingBalance,
			MonthlyInterestRate: loan.MonthlyInterestRate, PaymentType: loan.PaymentType,
			InstallmentDueDate: &dueDate, InterestThisMonth: inst.InterestAmount, AmountPaidThisMonth: paid,
			Status: status,
		})
	}
	return result
}
