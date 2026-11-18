package expenses

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/money"
)

// reports.go ports GastoService.resumoMensal/resumoAnual/listarAssinaturas — read from the real
// Java rather than guessed. Found missing while wiring the real frontend (these three power the
// main "Faturas & Gastos" tab, not a secondary view).

// ExpenseWithCategoryName is an Expense plus its category's name pre-resolved (a JOIN, not a
// per-row lookup) — "Sem categoria" when CategoryID is nil, mirrors GastoService's own fallback.
type ExpenseWithCategoryName struct {
	Expense
	CategoryName string
}

// CategorySummaryDTO is one category's total within a period — nil CategoryID groups every
// uncategorized entry under "Sem categoria" (mirrors GastoCategoriaResumoAcumulador).
type CategorySummaryDTO struct {
	CategoryID   *uuid.UUID `json:"categoryId"`
	CategoryName string     `json:"categoryName"`
	Total        string     `json:"total"`
}

// DaySummaryDTO is one calendar day's total within a monthly summary.
type DaySummaryDTO struct {
	Day   time.Time `json:"day"`
	Total string    `json:"total"`
}

// MonthSummaryDTO is one calendar month's total within an annual summary — always present for all
// 12 months, even at zero (GastoResumoAnualDto's bar chart needs all 12 bars).
type MonthSummaryDTO struct {
	Month int    `json:"month"`
	Total string `json:"total"`
	Count int    `json:"count"`
}

// HighlightDTO is a single standout entry (highest/lowest) within a period.
type HighlightDTO struct {
	Description  *string   `json:"description"`
	Amount       string    `json:"amount"`
	Date         time.Time `json:"date"`
	CategoryName string    `json:"categoryName"`
}

// MonthlySummaryDTO mirrors GastoResumoMensalDto.
type MonthlySummaryDTO struct {
	TotalAmount string               `json:"totalAmount"`
	EntryCount  int                  `json:"entryCount"`
	ByCategory  []CategorySummaryDTO `json:"byCategory"`
	ByDay       []DaySummaryDTO      `json:"byDay"`
	Highest     *HighlightDTO        `json:"highest"`
	Lowest      *HighlightDTO        `json:"lowest"`
}

// AnnualSummaryDTO mirrors GastoResumoAnualDto.
type AnnualSummaryDTO struct {
	TotalAmount string               `json:"totalAmount"`
	EntryCount  int                  `json:"entryCount"`
	ByCategory  []CategorySummaryDTO `json:"byCategory"`
	ByMonth     []MonthSummaryDTO    `json:"byMonth"`
	Highest     *HighlightDTO        `json:"highest"`
	Lowest      *HighlightDTO        `json:"lowest"`
}

// SubscriptionDTO mirrors AssinaturaResumoDto.
type SubscriptionDTO struct {
	Name                  string    `json:"name"`
	CurrentAmount         string    `json:"currentAmount"`
	AverageAmount         string    `json:"averageAmount"`
	LastCharge            time.Time `json:"lastCharge"`
	NextEstimatedCharge   time.Time `json:"nextEstimatedCharge"`
	Occurrences           int       `json:"occurrences"`
	PriceIncreaseDetected bool      `json:"priceIncreaseDetected"`
}

// subscriptionWindowMonths / priceIncreaseThreshold mirror MESES_JANELA_ASSINATURAS (12) and
// LIMIAR_AUMENTO_ASSINATURA ("0.10", a 10% jump) exactly.
const subscriptionWindowMonths = 12

const priceIncreaseThreshold = 0.10

// MonthlySummary mirrors GastoService.resumoMensal exactly, including its ByDay quirk: each entry
// projects onto the day-of-month of its ORIGINAL purchase date (OriginalDate), not its expense_date
// (competência, always the 1st for every line on one invoice) — otherwise every sparkline would be
// a flat line on day 1. Clamped to the target month's length; falls back to ExpenseDate when
// OriginalDate is nil.
func (s *Service) MonthlySummary(ctx context.Context, year, month int) (MonthlySummaryDTO, error) {
	from := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	toExclusive := from.AddDate(0, 1, 0)
	rows, err := s.repo.ListInRange(ctx, from, toExclusive)
	if err != nil {
		return MonthlySummaryDTO{}, fmt.Errorf("expenses: listing for monthly summary: %w", err)
	}

	byCategory := make(map[uuid.UUID]*categoryAccumulator)
	byDay := make(map[time.Time]string)
	total := money.Zero
	lastDay := from.AddDate(0, 1, -1).Day()

	for _, e := range rows {
		total, err = money.Add(total, e.Amount)
		if err != nil {
			return MonthlySummaryDTO{}, err
		}
		if err := accumulateCategory(byCategory, e); err != nil {
			return MonthlySummaryDTO{}, err
		}

		effectiveDay := from
		if e.OriginalDate != nil {
			day := e.OriginalDate.Day()
			if day > lastDay {
				day = lastDay
			}
			effectiveDay = time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		} else {
			effectiveDay = e.ExpenseDate
		}
		existing := byDay[effectiveDay]
		if existing == "" {
			existing = money.Zero
		}
		sum, err := money.Add(existing, e.Amount)
		if err != nil {
			return MonthlySummaryDTO{}, err
		}
		byDay[effectiveDay] = sum
	}

	days := make([]DaySummaryDTO, 0, len(byDay))
	for d, t := range byDay {
		days = append(days, DaySummaryDTO{Day: d, Total: t})
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Day.Before(days[j].Day) })

	highest, lowest, err := highlights(rows)
	if err != nil {
		return MonthlySummaryDTO{}, err
	}

	return MonthlySummaryDTO{
		TotalAmount: total, EntryCount: len(rows), ByCategory: sortedCategories(byCategory),
		ByDay: days, Highest: highest, Lowest: lowest,
	}, nil
}

// AnnualSummary mirrors GastoService.resumoAnual exactly, including always returning all 12 months.
func (s *Service) AnnualSummary(ctx context.Context, year int) (AnnualSummaryDTO, error) {
	from := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	toExclusive := time.Date(year+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	rows, err := s.repo.ListInRange(ctx, from, toExclusive)
	if err != nil {
		return AnnualSummaryDTO{}, fmt.Errorf("expenses: listing for annual summary: %w", err)
	}

	byCategory := make(map[uuid.UUID]*categoryAccumulator)
	totalByMonth := make(map[int]string)
	countByMonth := make(map[int]int)
	total := money.Zero

	for _, e := range rows {
		total, err = money.Add(total, e.Amount)
		if err != nil {
			return AnnualSummaryDTO{}, err
		}
		if err := accumulateCategory(byCategory, e); err != nil {
			return AnnualSummaryDTO{}, err
		}
		m := int(e.ExpenseDate.Month())
		existing := totalByMonth[m]
		if existing == "" {
			existing = money.Zero
		}
		sum, err := money.Add(existing, e.Amount)
		if err != nil {
			return AnnualSummaryDTO{}, err
		}
		totalByMonth[m] = sum
		countByMonth[m]++
	}

	months := make([]MonthSummaryDTO, 12)
	for m := 1; m <= 12; m++ {
		t := totalByMonth[m]
		if t == "" {
			t = money.Zero
		}
		months[m-1] = MonthSummaryDTO{Month: m, Total: t, Count: countByMonth[m]}
	}

	highest, lowest, err := highlights(rows)
	if err != nil {
		return AnnualSummaryDTO{}, err
	}

	return AnnualSummaryDTO{
		TotalAmount: total, EntryCount: len(rows), ByCategory: sortedCategories(byCategory),
		ByMonth: months, Highest: highest, Lowest: lowest,
	}, nil
}

// YearSummaryDTO is one calendar year's total within a multi-year range summary.
type YearSummaryDTO struct {
	Year  int    `json:"year"`
	Total string `json:"total"`
}

// RangeSummaryDTO is a total + by-category + by-year breakdown over an arbitrary [yearStart,
// yearEnd] range — used by internal/reports' combined financial report (RELATORIO_FINANCEIRO),
// which needs a multi-year span rather than AnnualSummary's fixed single calendar year.
type RangeSummaryDTO struct {
	TotalAmount string               `json:"totalAmount"`
	ByCategory  []CategorySummaryDTO `json:"byCategory"`
	ByYear      []YearSummaryDTO     `json:"byYear"`
}

// RangeSummary sums every expense in [yearStart-01-01, yearEnd+1-01-01) once (not once per year in
// the range, mirroring RelatorioFinanceiroService.gerar's single-query-per-range discipline),
// grouping by category and by year in the same pass.
func (s *Service) RangeSummary(ctx context.Context, yearStart, yearEnd int) (RangeSummaryDTO, error) {
	from := time.Date(yearStart, time.January, 1, 0, 0, 0, 0, time.UTC)
	toExclusive := time.Date(yearEnd+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	rows, err := s.repo.ListInRange(ctx, from, toExclusive)
	if err != nil {
		return RangeSummaryDTO{}, fmt.Errorf("expenses: listing for range summary: %w", err)
	}

	byCategory := make(map[uuid.UUID]*categoryAccumulator)
	totalByYear := make(map[int]string)
	total := money.Zero

	for _, e := range rows {
		total, err = money.Add(total, e.Amount)
		if err != nil {
			return RangeSummaryDTO{}, err
		}
		if err := accumulateCategory(byCategory, e); err != nil {
			return RangeSummaryDTO{}, err
		}
		y := e.ExpenseDate.Year()
		existing := totalByYear[y]
		if existing == "" {
			existing = money.Zero
		}
		sum, err := money.Add(existing, e.Amount)
		if err != nil {
			return RangeSummaryDTO{}, err
		}
		totalByYear[y] = sum
	}

	years := make([]YearSummaryDTO, 0, yearEnd-yearStart+1)
	for y := yearStart; y <= yearEnd; y++ {
		t := totalByYear[y]
		if t == "" {
			t = money.Zero
		}
		years = append(years, YearSummaryDTO{Year: y, Total: t})
	}

	return RangeSummaryDTO{TotalAmount: total, ByCategory: sortedCategories(byCategory), ByYear: years}, nil
}

// Subscriptions mirrors GastoService.listarAssinaturas exactly: every Recurring=true entry in the
// last subscriptionWindowMonths months, grouped by trimmed/lowercased Description (entries with no
// description at all are excluded — can't group two "unnamed" subscriptions as the same one), most
// recent charge first.
func (s *Service) Subscriptions(ctx context.Context, now time.Time) ([]SubscriptionDTO, error) {
	since := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -subscriptionWindowMonths, 0)
	rows, err := s.repo.ListRecurringSince(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("expenses: listing recurring for subscriptions: %w", err)
	}

	byName := make(map[string][]ExpenseWithCategoryName)
	var order []string
	for _, e := range rows {
		if e.Description == nil {
			continue
		}
		desc := strings.TrimSpace(*e.Description)
		if desc == "" {
			continue
		}
		key := strings.ToLower(desc)
		if _, seen := byName[key]; !seen {
			order = append(order, key)
		}
		byName[key] = append(byName[key], e)
	}

	result := make([]SubscriptionDTO, 0, len(order))
	for _, key := range order {
		occurrences := byName[key]
		// Each caller's ListRecurringSince must already order most-recent-first (mirrors the
		// repository query's ORDER BY data DESC) — occurrences[0] is relied on as "most recent".
		mostRecent := occurrences[0]
		// The Recurring flag alone is too weak to mean "subscription" for an imported invoice line
		// — see looksLikeSubscription (Uber was showing up here with 101 charges in a month). But a
		// manually entered expense (DocumentID == nil) with Recurring == true is the user's own,
		// direct statement — curator.markRepeatedInvoiceExpensesRecurring never touches those rows
		// (see its own doc comment), so nothing else could have set the flag. Trust it immediately,
		// without waiting for 3 months of history to "prove" what the user already said.
		if mostRecent.DocumentID != nil && !looksLikeSubscription(occurrences, now) {
			continue
		}
		sub, err := toSubscription(mostRecent, occurrences, now)
		if err != nil {
			return nil, err
		}
		result = append(result, sub)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastCharge.After(result[j].LastCharge) })
	return result, nil
}

const (
	// A subscription has to have actually recurred: two months is a coincidence, three is a
	// pattern. (An invoice import can also put two months of the same merchant in view at once.)
	minSubscriptionMonths = 3
	// Roughly one charge per month. A subscription bills monthly; a merchant you simply use a lot
	// bills whenever you use it. The +1 slack absorbs a proration or an annual-plan charge landing
	// beside a monthly one, without admitting "Uber, 101 charges".
	maxChargesPerMonthSlack = 1
	// Amounts have to be stable — "the same value", as the user put it. Subscriptions do change
	// price (that's what PriceIncreaseDetected reports), so a 20% band tolerates a real bump
	// while rejecting the spread of iFood orders / vet visits.
	maxSubscriptionAmountRatio = 1.2
	// A subscription that stopped billing is not a current subscription. If the newest charge is
	// more than one competence month behind `now`, it has lapsed — drop it, rather than showing a
	// "next charge" date that's already in the past (which then reads as a data bug).
	maxSubscriptionStaleMonths = 1
)

// looksLikeSubscription decides whether a group of same-description charges is a subscription
// rather than a merchant that simply appears often.
//
// The Recurring flag these rows carry means only "this description appeared in at least two
// competence months" (see curator.markRepeatedInvoiceExpensesRecurring), which is true of every
// habitual merchant. In production that surfaced Uber (101 charges in one month), "iFood -
// Rodrigo" (2 charges) and Nuuvem (2 charges) as subscriptions, each with a meaningless
// "acima da média" badge. A subscription is what the user described: the same amount, in
// consecutive months, still billing now — so all of those are checked here. occurrences is
// most-recent-first; now is the reference date.
func looksLikeSubscription(occurrences []ExpenseWithCategoryName, now time.Time) bool {
	if len(occurrences) < minSubscriptionMonths {
		return false
	}

	months := make(map[string]struct{}, len(occurrences))
	var min, max *big.Rat
	var earliest, latest time.Time
	for i, e := range occurrences {
		months[e.ExpenseDate.Format("2006-01")] = struct{}{}
		if i == 0 || e.ExpenseDate.Before(earliest) {
			earliest = e.ExpenseDate
		}
		if i == 0 || e.ExpenseDate.After(latest) {
			latest = e.ExpenseDate
		}

		amount, err := money.Parse(e.Amount)
		if err != nil || amount.Sign() <= 0 {
			// A refund or an unparseable amount says nothing about billing regularity; ignore it
			// for the spread, but it still counted as an occurrence above.
			continue
		}
		if min == nil || amount.Cmp(min) < 0 {
			min = amount
		}
		if max == nil || amount.Cmp(max) > 0 {
			max = amount
		}
	}

	if len(months) < minSubscriptionMonths {
		return false
	}
	// Still active: the newest charge can't be more than a month behind now, or it has lapsed.
	if competenceMonthsBetween(latest, now) > maxSubscriptionStaleMonths {
		return false
	}
	// Consecutive: the billing months must be contiguous (no skipped month). Feb/Mar/Apr yes,
	// Feb/Apr/Aug no — that's a merchant used sporadically, not a monthly plan.
	if competenceMonthsBetween(earliest, latest)+1 != len(months) {
		return false
	}
	// Billed about monthly, not every time you feel like a ride.
	if len(occurrences) > len(months)+maxChargesPerMonthSlack {
		return false
	}
	if min == nil || max == nil || min.Sign() <= 0 {
		return false
	}
	limit := new(big.Rat).Mul(min, new(big.Rat).SetFloat64(maxSubscriptionAmountRatio))
	return max.Cmp(limit) <= 0
}

// competenceMonthsBetween counts whole calendar months from a to b (negative if b is earlier).
func competenceMonthsBetween(a, b time.Time) int {
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
}

func toSubscription(mostRecent ExpenseWithCategoryName, occurrences []ExpenseWithCategoryName, now time.Time) (SubscriptionDTO, error) {
	total := money.Zero
	var err error
	for _, o := range occurrences {
		total, err = money.Add(total, o.Amount)
		if err != nil {
			return SubscriptionDTO{}, err
		}
	}
	average, err := money.Div(total, int64(len(occurrences)), 4)
	if err != nil {
		return SubscriptionDTO{}, err
	}

	increaseDetected := false
	if len(occurrences) > 1 {
		avgSign, err := money.Sign(average)
		if err != nil {
			return SubscriptionDTO{}, err
		}
		if avgSign > 0 {
			diff, err := money.Sub(mostRecent.Amount, average)
			if err != nil {
				return SubscriptionDTO{}, err
			}
			ratio, err := ratioOf(diff, average)
			if err != nil {
				return SubscriptionDTO{}, err
			}
			increaseDetected = ratio > priceIncreaseThreshold
		}
	}

	avgRounded, err := money.Add(average, money.Zero) // normalize to 2 decimals for display
	if err != nil {
		return SubscriptionDTO{}, err
	}

	return SubscriptionDTO{
		Name: strings.TrimSpace(*mostRecent.Description), CurrentAmount: mostRecent.Amount,
		AverageAmount: avgRounded, LastCharge: mostRecent.ExpenseDate,
		NextEstimatedCharge: nextChargeOnOrAfter(mostRecent.ExpenseDate, now), Occurrences: len(occurrences),
		PriceIncreaseDetected: increaseDetected,
	}, nil
}

// nextChargeOnOrAfter returns the first monthly anniversary of `last` (k >= 1 months later) that
// is not before `ref`, keeping the day-of-month and clamping to the target month's last day
// (Jan 31 -> Feb 28). Rolling forward past `ref` — not just "last + 1 month" — keeps the estimate
// out of the past even when the newest charge is a few weeks old.
func nextChargeOnOrAfter(last, ref time.Time) time.Time {
	day := last.Day()
	baseMonth := last.Year()*12 + int(last.Month()) - 1
	refDate := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, last.Location())
	for k := 1; ; k++ {
		total := baseMonth + k
		year, month := total/12, time.Month(total%12+1)
		lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, last.Location()).Day()
		d := day
		if d > lastDay {
			d = lastDay
		}
		candidate := time.Date(year, month, d, 0, 0, 0, 0, last.Location())
		if !candidate.Before(refDate) {
			return candidate
		}
	}
}

// ratioOf computes diff/base as a float64 — fine here (a boolean threshold comparison, not a
// stored/displayed monetary value, unlike every other money computation in this file).
func ratioOf(diff, base string) (float64, error) {
	d, err := money.Parse(diff)
	if err != nil {
		return 0, err
	}
	b, err := money.Parse(base)
	if err != nil {
		return 0, err
	}
	f, _ := new(big.Rat).Quo(d, b).Float64()
	return f, nil
}

type categoryAccumulator struct {
	name  string
	total string
}

func accumulateCategory(acc map[uuid.UUID]*categoryAccumulator, e ExpenseWithCategoryName) error {
	key := uuid.Nil
	if e.CategoryID != nil {
		key = *e.CategoryID
	}
	a, ok := acc[key]
	if !ok {
		a = &categoryAccumulator{name: e.CategoryName, total: money.Zero}
		acc[key] = a
	}
	sum, err := money.Add(a.total, e.Amount)
	if err != nil {
		return err
	}
	a.total = sum
	return nil
}

func sortedCategories(acc map[uuid.UUID]*categoryAccumulator) []CategorySummaryDTO {
	result := make([]CategorySummaryDTO, 0, len(acc))
	for id, a := range acc {
		var catID *uuid.UUID
		if id != uuid.Nil {
			v := id
			catID = &v
		}
		result = append(result, CategorySummaryDTO{CategoryID: catID, CategoryName: a.name, Total: a.total})
	}
	sort.Slice(result, func(i, j int) bool {
		cmp, _ := money.Compare(result[j].Total, result[i].Total) // largest total first
		return cmp < 0
	})
	return result
}

// highlights returns the highest and lowest entries — "lowest" ignores non-positive amounts
// (refunds/adjustments), mirroring menorGasto's own filter exactly.
func highlights(rows []ExpenseWithCategoryName) (highest, lowest *HighlightDTO, err error) {
	for _, e := range rows {
		if highest == nil {
			h := toHighlight(e)
			highest = &h
		} else {
			cmp, cErr := money.Compare(e.Amount, highest.Amount)
			if cErr != nil {
				return nil, nil, cErr
			}
			if cmp > 0 {
				h := toHighlight(e)
				highest = &h
			}
		}
		sign, sErr := money.Sign(e.Amount)
		if sErr != nil {
			return nil, nil, sErr
		}
		if sign > 0 {
			if lowest == nil {
				l := toHighlight(e)
				lowest = &l
			} else {
				cmp, cErr := money.Compare(e.Amount, lowest.Amount)
				if cErr != nil {
					return nil, nil, cErr
				}
				if cmp < 0 {
					l := toHighlight(e)
					lowest = &l
				}
			}
		}
	}
	return highest, lowest, nil
}

func toHighlight(e ExpenseWithCategoryName) HighlightDTO {
	return HighlightDTO{Description: e.Description, Amount: e.Amount, Date: e.ExpenseDate, CategoryName: e.CategoryName}
}
