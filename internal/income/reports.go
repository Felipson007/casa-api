package income

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/money"
)

// reports.go ports ReceitaService.resumoMensal/resumoAnual — read from the real Java rather than
// guessed. Found missing while wiring the real frontend (powers the main "Holerite & Receitas"
// tab). Mirrors internal/expenses/reports.go's structure exactly, with the one real difference
// Java has: proventos (positive amounts) and descontos (negative — INSS, IRRF, etc.) are summed
// and highlighted separately, not just a single highest/lowest.

// EntryWithCategoryName is an Entry plus its category's name pre-resolved.
type EntryWithCategoryName struct {
	Entry
	CategoryName string
}

// CategorySummaryDTO is one category's total within a period.
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
// 12 months, even at zero.
type MonthSummaryDTO struct {
	Month int    `json:"month"`
	Total string `json:"total"`
	Count int    `json:"count"`
}

// HighlightDTO is a single standout entry within a period.
type HighlightDTO struct {
	Description  *string   `json:"description"`
	Amount       string    `json:"amount"`
	Date         time.Time `json:"date"`
	CategoryName string    `json:"categoryName"`
}

// MonthlySummaryDTO mirrors ReceitaResumoMensalDto.
type MonthlySummaryDTO struct {
	NetTotal         string               `json:"netTotal"`
	GrossEarnings    string               `json:"grossEarnings"`
	TotalDeductions  string               `json:"totalDeductions"`
	EntryCount       int                  `json:"entryCount"`
	ByCategory       []CategorySummaryDTO `json:"byCategory"`
	ByDay            []DaySummaryDTO      `json:"byDay"`
	HighestEarning   *HighlightDTO        `json:"highestEarning"`
	HighestDeduction *HighlightDTO        `json:"highestDeduction"`

	// USDTotal/ConvertedBRLTotal/AvgExchangeRate cover only the entries with USDAmount set this
	// month (a foreign-currency salary — see Entry's doc comment); empty when none exist, rather
	// than showing misleading zeros. AvgExchangeRate is the effective rate (ConvertedBRLTotal ÷
	// USDTotal), not an average of each entry's own rate — correct when a month has more than one
	// conversion at different rates.
	USDTotal          string `json:"usdTotal,omitempty"`
	ConvertedBRLTotal string `json:"convertedBrlTotal,omitempty"`
	AvgExchangeRate   string `json:"avgExchangeRate,omitempty"`
}

// AnnualSummaryDTO mirrors ReceitaResumoAnualDto.
type AnnualSummaryDTO struct {
	NetTotal         string               `json:"netTotal"`
	GrossEarnings    string               `json:"grossEarnings"`
	TotalDeductions  string               `json:"totalDeductions"`
	EntryCount       int                  `json:"entryCount"`
	ByCategory       []CategorySummaryDTO `json:"byCategory"`
	ByMonth          []MonthSummaryDTO    `json:"byMonth"`
	HighestEarning   *HighlightDTO        `json:"highestEarning"`
	HighestDeduction *HighlightDTO        `json:"highestDeduction"`
}

// MonthlySummary mirrors ReceitaService.resumoMensal exactly, including the ByDay projection quirk
// (see expenses.Service.MonthlySummary's doc comment — identical reasoning, same "data always the
// 1st for a given holerite" problem).
func (s *Service) MonthlySummary(ctx context.Context, year, month int) (MonthlySummaryDTO, error) {
	from := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	toExclusive := from.AddDate(0, 1, 0)
	rows, err := s.repo.ListInRange(ctx, from, toExclusive)
	if err != nil {
		return MonthlySummaryDTO{}, fmt.Errorf("income: listing for monthly summary: %w", err)
	}

	byCategory := make(map[uuid.UUID]*categoryAccumulator)
	byDay := make(map[time.Time]string)
	gross, deductions := money.Zero, money.Zero
	lastDay := from.AddDate(0, 1, -1).Day()
	usd := newUSDAccumulator()

	for _, e := range rows {
		sign, err := money.Sign(e.Amount)
		if err != nil {
			return MonthlySummaryDTO{}, err
		}
		if sign >= 0 {
			gross, err = money.Add(gross, e.Amount)
		} else {
			deductions, err = money.Add(deductions, e.Amount)
		}
		if err != nil {
			return MonthlySummaryDTO{}, err
		}
		if err := accumulateCategory(byCategory, e); err != nil {
			return MonthlySummaryDTO{}, err
		}
		if err := usd.add(e.USDAmount, e.Amount); err != nil {
			return MonthlySummaryDTO{}, err
		}

		effectiveDay := e.IncomeDate
		if e.OriginalDate != nil {
			day := e.OriginalDate.Day()
			if day > lastDay {
				day = lastDay
			}
			effectiveDay = time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
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

	net, err := money.Add(gross, deductions)
	if err != nil {
		return MonthlySummaryDTO{}, err
	}
	highestEarning, highestDeduction, err := highlights(rows)
	if err != nil {
		return MonthlySummaryDTO{}, err
	}
	usdTotal, convertedBRLTotal, avgExchangeRate, err := usd.summary()
	if err != nil {
		return MonthlySummaryDTO{}, err
	}

	return MonthlySummaryDTO{
		NetTotal: net, GrossEarnings: gross, TotalDeductions: deductions, EntryCount: len(rows),
		ByCategory: sortedCategories(byCategory), ByDay: days,
		HighestEarning: highestEarning, HighestDeduction: highestDeduction,
		USDTotal: usdTotal, ConvertedBRLTotal: convertedBRLTotal, AvgExchangeRate: avgExchangeRate,
	}, nil
}

// AnnualSummary mirrors ReceitaService.resumoAnual exactly, including always returning all 12 months.
func (s *Service) AnnualSummary(ctx context.Context, year int) (AnnualSummaryDTO, error) {
	from := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	toExclusive := time.Date(year+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	rows, err := s.repo.ListInRange(ctx, from, toExclusive)
	if err != nil {
		return AnnualSummaryDTO{}, fmt.Errorf("income: listing for annual summary: %w", err)
	}

	byCategory := make(map[uuid.UUID]*categoryAccumulator)
	totalByMonth := make(map[int]string)
	countByMonth := make(map[int]int)
	gross, deductions := money.Zero, money.Zero

	for _, e := range rows {
		sign, err := money.Sign(e.Amount)
		if err != nil {
			return AnnualSummaryDTO{}, err
		}
		if sign >= 0 {
			gross, err = money.Add(gross, e.Amount)
		} else {
			deductions, err = money.Add(deductions, e.Amount)
		}
		if err != nil {
			return AnnualSummaryDTO{}, err
		}
		if err := accumulateCategory(byCategory, e); err != nil {
			return AnnualSummaryDTO{}, err
		}
		m := int(e.IncomeDate.Month())
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

	net, err := money.Add(gross, deductions)
	if err != nil {
		return AnnualSummaryDTO{}, err
	}
	highestEarning, highestDeduction, err := highlights(rows)
	if err != nil {
		return AnnualSummaryDTO{}, err
	}

	return AnnualSummaryDTO{
		NetTotal: net, GrossEarnings: gross, TotalDeductions: deductions, EntryCount: len(rows),
		ByCategory: sortedCategories(byCategory), ByMonth: months,
		HighestEarning: highestEarning, HighestDeduction: highestDeduction,
	}, nil
}

// YearSummaryDTO is one calendar year's net total within a multi-year range summary.
type YearSummaryDTO struct {
	Year  int    `json:"year"`
	Total string `json:"total"`
}

// RangeSummaryDTO is a net total + by-category + by-year breakdown over an arbitrary [yearStart,
// yearEnd] range — used by internal/reports' combined financial report (RELATORIO_FINANCEIRO).
type RangeSummaryDTO struct {
	NetTotal   string               `json:"netTotal"`
	ByCategory []CategorySummaryDTO `json:"byCategory"`
	ByYear     []YearSummaryDTO     `json:"byYear"`
}

// RangeSummary sums every income entry in [yearStart-01-01, yearEnd+1-01-01) once, grouping by
// category and by year in the same pass — mirrors expenses.Service.RangeSummary.
func (s *Service) RangeSummary(ctx context.Context, yearStart, yearEnd int) (RangeSummaryDTO, error) {
	from := time.Date(yearStart, time.January, 1, 0, 0, 0, 0, time.UTC)
	toExclusive := time.Date(yearEnd+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	rows, err := s.repo.ListInRange(ctx, from, toExclusive)
	if err != nil {
		return RangeSummaryDTO{}, fmt.Errorf("income: listing for range summary: %w", err)
	}

	byCategory := make(map[uuid.UUID]*categoryAccumulator)
	totalByYear := make(map[int]string)
	net := money.Zero

	for _, e := range rows {
		net, err = money.Add(net, e.Amount)
		if err != nil {
			return RangeSummaryDTO{}, err
		}
		if err := accumulateCategory(byCategory, e); err != nil {
			return RangeSummaryDTO{}, err
		}
		y := e.IncomeDate.Year()
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

	return RangeSummaryDTO{NetTotal: net, ByCategory: sortedCategories(byCategory), ByYear: years}, nil
}

type categoryAccumulator struct {
	name  string
	total string
}

func accumulateCategory(acc map[uuid.UUID]*categoryAccumulator, e EntryWithCategoryName) error {
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

// highlights returns the largest positive entry (highestEarning) and the most-negative entry
// (highestDeduction, largest magnitude) — mirrors maiorProvento/maiorDesconto exactly.
func highlights(rows []EntryWithCategoryName) (highestEarning, highestDeduction *HighlightDTO, err error) {
	for _, e := range rows {
		sign, sErr := money.Sign(e.Amount)
		if sErr != nil {
			return nil, nil, sErr
		}
		if sign > 0 {
			if highestEarning == nil {
				h := toHighlight(e)
				highestEarning = &h
			} else if cmp, cErr := money.Compare(e.Amount, highestEarning.Amount); cErr != nil {
				return nil, nil, cErr
			} else if cmp > 0 {
				h := toHighlight(e)
				highestEarning = &h
			}
		} else if sign < 0 {
			if highestDeduction == nil {
				h := toHighlight(e)
				highestDeduction = &h
			} else if cmp, cErr := money.Compare(e.Amount, highestDeduction.Amount); cErr != nil {
				return nil, nil, cErr
			} else if cmp < 0 {
				h := toHighlight(e)
				highestDeduction = &h
			}
		}
	}
	return highestEarning, highestDeduction, nil
}

func toHighlight(e EntryWithCategoryName) HighlightDTO {
	return HighlightDTO{Description: e.Description, Amount: e.Amount, Date: e.IncomeDate, CategoryName: e.CategoryName}
}

// usdAccumulator sums USDAmount and the paired BRL Amount across a month's foreign-currency income
// entries, then derives the effective conversion rate — big.Rat rather than the money package
// (numeric(15,2)-as-string) because AvgExchangeRate needs 4 decimal places (numeric(10,4), to match
// the exchange_rate column) and is a division, not an addition.
type usdAccumulator struct {
	any      bool
	usdTotal *big.Rat
	brlTotal *big.Rat
}

func newUSDAccumulator() *usdAccumulator {
	return &usdAccumulator{usdTotal: new(big.Rat), brlTotal: new(big.Rat)}
}

// add folds in one entry — a no-op unless usdAmount is set (most entries aren't foreign-currency).
func (a *usdAccumulator) add(usdAmount *string, brlAmount string) error {
	if usdAmount == nil {
		return nil
	}
	usd, ok := new(big.Rat).SetString(*usdAmount)
	if !ok {
		return fmt.Errorf("income: parsing usd_amount %q", *usdAmount)
	}
	brl, ok := new(big.Rat).SetString(brlAmount)
	if !ok {
		return fmt.Errorf("income: parsing amount %q", brlAmount)
	}
	a.usdTotal.Add(a.usdTotal, usd)
	a.brlTotal.Add(a.brlTotal, brl)
	a.any = true
	return nil
}

// summary returns ("", "", "", nil) when no entry in the period had a USD amount — MonthlySummary
// then omits all three JSON fields (omitempty) rather than showing misleading zeros.
func (a *usdAccumulator) summary() (usdTotal, convertedBRLTotal, avgExchangeRate string, err error) {
	if !a.any {
		return "", "", "", nil
	}
	usdTotal = a.usdTotal.FloatString(2)
	convertedBRLTotal = a.brlTotal.FloatString(2)
	if a.usdTotal.Sign() == 0 {
		return usdTotal, convertedBRLTotal, "", nil
	}
	rate := new(big.Rat).Quo(a.brlTotal, a.usdTotal)
	return usdTotal, convertedBRLTotal, rate.FloatString(4), nil
}
