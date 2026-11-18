package curator

import (
	"math/big"
	"sort"
	"strings"

	"github.com/atlas/atlas-go/internal/categories"
	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/finance"
	"github.com/atlas/atlas-go/internal/goals"
	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/loans"
)

// Digest is the consolidated, pre-aggregated view of a person's data handed to the model. Every
// count, sum, average and ranking the model would otherwise have to derive from raw rows is already
// computed here — the model that saw the raw lists routinely miscounted ("24 active loans" for 22,
// "R$ 45.200" for 39.400). It is also far smaller than the raw dumps it replaces.
type Digest struct {
	Date       string         `json:"date"`
	Loans      loansDigest    `json:"loans"`
	Expenses   spendingDigest `json:"expenses"`
	Income     incomeDigest   `json:"income"`
	Balances   balancesDigest `json:"balances"`
	Goals      []goalRow      `json:"goals"`
	MonthlyNet netDigest      `json:"net"`
	Health     healthDigest   `json:"health"`

	// Inputs for the mechanical reclassification task only — not analysis material.
	ExpenseCategories  []string         `json:"expenseCategories"`
	UnreviewedExpenses []unreviewedItem `json:"unreviewedExpenses"`
}

type loansDigest struct {
	ByStatus                     []statusBucket `json:"byStatus"`
	ActiveCount                  int            `json:"activeCount"`
	ActivePrincipalTotal         string         `json:"activePrincipalTotal"`
	ActiveOutstandingTotal       string         `json:"activeOutstandingTotal"`
	ActiveWeightedMonthlyRatePct string         `json:"activeWeightedMonthlyRatePct"`
	ActiveByMonthlyRate          []rateBucket   `json:"activeByMonthlyRatePct"`
	ActiveByBorrower             []borrowerRow  `json:"activeByBorrower"`
}

type statusBucket struct {
	Status           string `json:"status"`
	Count            int    `json:"count"`
	PrincipalTotal   string `json:"principalTotal"`
	OutstandingTotal string `json:"outstandingTotal"`
}

type rateBucket struct {
	MonthlyRatePct string   `json:"monthlyRatePct"`
	Count          int      `json:"count"`
	PrincipalTotal string   `json:"principalTotal"`
	Borrowers      []string `json:"borrowers"`
}

type borrowerRow struct {
	Name             string   `json:"name"`
	ActiveLoans      int      `json:"activeLoans"`
	PrincipalTotal   string   `json:"principalTotal"`
	OutstandingTotal string   `json:"outstandingTotal"`
	MonthlyRatesPct  []string `json:"monthlyRatesPct"`
}

type spendingDigest struct {
	CurrentMonth        periodSpending `json:"currentMonth"`
	PreviousMonth       periodSpending `json:"previousMonth"`
	MonthOverMonthDelta string         `json:"monthOverMonthDelta"`
	YearToDate          periodSpending `json:"yearToDate"`
	Subscriptions       subsDigest     `json:"subscriptions"`
	TopMerchantsYTD     []merchantRow  `json:"topMerchantsYTD"`
	UnreviewedCount     int            `json:"unreviewedCount"`
}

type periodSpending struct {
	Label      string   `json:"label"`
	Total      string   `json:"total"`
	Count      int      `json:"count"`
	ByCategory []catRow `json:"byCategory"`
}

type catRow struct {
	Category string `json:"category"`
	Total    string `json:"total"`
}

type subsDigest struct {
	Count        int      `json:"count"`
	MonthlyTotal string   `json:"monthlyTotal"`
	Items        []subRow `json:"items"`
}

type subRow struct {
	Name               string `json:"name"`
	Amount             string `json:"amount"`
	NextChargeEstimate string `json:"nextChargeEstimate"`
}

type merchantRow struct {
	Description string `json:"description"`
	Total       string `json:"total"`
	Count       int    `json:"count"`
}

type incomeDigest struct {
	CurrentMonthNet        string   `json:"currentMonthNet"`
	PreviousMonthNet       string   `json:"previousMonthNet"`
	YearToDateNet          string   `json:"yearToDateNet"`
	CurrentMonthByCategory []catRow `json:"currentMonthByCategory"`

	// ForeignSalary is only present when the current month has at least one USD-paid income entry
	// (income.Entry.USDAmount — a foreign employer, converted to BRL, e.g. via Wise). Omitted
	// entirely otherwise, so the model never sees a misleading zeroed-out block for the common case.
	ForeignSalary *foreignSalaryDigest `json:"foreignSalaryCurrentMonth,omitempty"`
}

type foreignSalaryDigest struct {
	USDReceived      string `json:"usdReceived"`
	BRLTotal         string `json:"brlTotal"`
	EffectiveRateBRL string `json:"effectiveRateBrlPerUsd"`
}

type balancesDigest struct {
	GrandTotal string       `json:"grandTotal"`
	ByType     []balTypeRow `json:"byType"`
	Stalest    *balStale    `json:"stalest,omitempty"`
}

type balTypeRow struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
	Total string `json:"total"`
}

type balStale struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	UpdatedOn string `json:"updatedOn"`
}

type goalRow struct {
	Name                string `json:"name"`
	Kind                string `json:"kind"`
	Status              string `json:"status"`
	TargetAmount        string `json:"targetAmount"`
	TargetDate          string `json:"targetDate,omitempty"`
	Accumulated         string `json:"accumulated"`
	Gap                 string `json:"gap"`
	ProgressPct         string `json:"progressPct"`
	MonthlyContribution string `json:"monthlyContribution,omitempty"`
	MonthsToTarget      *int   `json:"monthsToTarget,omitempty"`
	OnTrackForDate      *bool  `json:"onTrackForDate,omitempty"`
}

type netDigest struct {
	CurrentMonth  string `json:"currentMonth"`
	PreviousMonth string `json:"previousMonth"`
	YearToDate    string `json:"yearToDate"`
}

type healthDigest struct {
	RecordCount    int          `json:"recordCount"`
	NeedsAttention []healthFlag `json:"needsAttention"`
}

type healthFlag struct {
	Person     string `json:"person"`
	Type       string `json:"type"`
	Assessment string `json:"assessment"`
	Value      string `json:"value,omitempty"`
	Unit       string `json:"unit,omitempty"`
	Date       string `json:"date"`
}

type unreviewedItem struct {
	ExpenseID       string `json:"expenseId"`
	Description     string `json:"description"`
	Amount          string `json:"amount"`
	Date            string `json:"date"`
	CurrentCategory string `json:"currentCategory"`
}

// digestInput bundles everything buildDigest needs; RunDaily loads each piece from its module's
// own service (aggregate queries, not raw-row dumps).
type digestInput struct {
	date          string
	loans         []loans.LoanDTO
	clientNames   map[string]string
	expenseItems  []expenses.ExpenseDTO
	expCurrent    expenses.MonthlySummaryDTO
	expPrevious   expenses.MonthlySummaryDTO
	expYTD        expenses.AnnualSummaryDTO
	subscriptions []expenses.SubscriptionDTO
	incCurrent    income.MonthlySummaryDTO
	incPrevious   income.MonthlySummaryDTO
	incYTD        income.AnnualSummaryDTO
	balances      []finance.BalanceDTO
	goals         []goals.GoalDTO
	healthItems   []health.RecordDTO
	personNames   map[string]string
	expenseCats   []categories.CategoryDTO
}

const (
	maxRateBucketBorrowers = 8
	maxBorrowerRows        = 15
	maxTopMerchants        = 12
	maxUnreviewedItems     = 40
	maxHealthFlags         = 20
)

func buildDigest(in digestInput) Digest {
	return Digest{
		Date:               in.date,
		Loans:              loansDigestOf(in.loans, in.clientNames),
		Expenses:           spendingDigestOf(in),
		Income:             incomeDigestOf(in),
		Balances:           balancesDigestOf(in.balances),
		Goals:              goalsDigestOf(in.goals),
		MonthlyNet:         netDigestOf(in),
		Health:             healthDigestOf(in.healthItems, in.personNames),
		ExpenseCategories:  categoryNames(in.expenseCats),
		UnreviewedExpenses: unreviewedExpensesOf(in.expenseItems, in.expenseCats),
	}
}

func loansDigestOf(items []loans.LoanDTO, clientNames map[string]string) loansDigest {
	type acc struct {
		count       int
		principal   *big.Rat
		outstanding *big.Rat
	}
	byStatus := map[string]*acc{}
	rateGroups := map[string]*struct {
		count     int
		principal *big.Rat
		borrowers map[string]bool
	}{}
	borrowerGroups := map[string]*struct {
		count       int
		principal   *big.Rat
		outstanding *big.Rat
		rates       map[string]bool
	}{}
	weightedNumerator := new(big.Rat)
	activePrincipal := new(big.Rat)
	activeOutstanding := new(big.Rat)
	activeCount := 0

	for _, l := range items {
		st := string(l.Status)
		a := byStatus[st]
		if a == nil {
			a = &acc{principal: new(big.Rat), outstanding: new(big.Rat)}
			byStatus[st] = a
		}
		a.count++
		a.principal.Add(a.principal, rat(l.PrincipalAmount))
		a.outstanding.Add(a.outstanding, rat(l.OutstandingBalance))

		if l.Status != loans.StatusActive {
			continue
		}
		activeCount++
		principal := rat(l.PrincipalAmount)
		activePrincipal.Add(activePrincipal, principal)
		activeOutstanding.Add(activeOutstanding, rat(l.OutstandingBalance))
		ratePct := money(rat(l.MonthlyInterestRate))
		weightedNumerator.Add(weightedNumerator, new(big.Rat).Mul(rat(l.MonthlyInterestRate), principal))

		name := clientNames[l.ClientID.String()]
		if name == "" {
			name = "(sem nome)"
		}

		rg := rateGroups[ratePct]
		if rg == nil {
			rg = &struct {
				count     int
				principal *big.Rat
				borrowers map[string]bool
			}{principal: new(big.Rat), borrowers: map[string]bool{}}
			rateGroups[ratePct] = rg
		}
		rg.count++
		rg.principal.Add(rg.principal, principal)
		rg.borrowers[name] = true

		bg := borrowerGroups[name]
		if bg == nil {
			bg = &struct {
				count       int
				principal   *big.Rat
				outstanding *big.Rat
				rates       map[string]bool
			}{principal: new(big.Rat), outstanding: new(big.Rat), rates: map[string]bool{}}
			borrowerGroups[name] = bg
		}
		bg.count++
		bg.principal.Add(bg.principal, principal)
		bg.outstanding.Add(bg.outstanding, rat(l.OutstandingBalance))
		bg.rates[ratePct] = true
	}

	d := loansDigest{
		ActiveCount:            activeCount,
		ActivePrincipalTotal:   money(activePrincipal),
		ActiveOutstandingTotal: money(activeOutstanding),
	}
	if activePrincipal.Sign() > 0 {
		d.ActiveWeightedMonthlyRatePct = money(new(big.Rat).Quo(weightedNumerator, activePrincipal))
	} else {
		d.ActiveWeightedMonthlyRatePct = "0.00"
	}

	for st, a := range byStatus {
		d.ByStatus = append(d.ByStatus, statusBucket{
			Status: st, Count: a.count,
			PrincipalTotal: money(a.principal), OutstandingTotal: money(a.outstanding),
		})
	}
	sort.Slice(d.ByStatus, func(i, j int) bool { return d.ByStatus[i].Count > d.ByStatus[j].Count })

	for rate, rg := range rateGroups {
		d.ActiveByMonthlyRate = append(d.ActiveByMonthlyRate, rateBucket{
			MonthlyRatePct: rate, Count: rg.count, PrincipalTotal: money(rg.principal),
			Borrowers: cappedSortedKeys(rg.borrowers, maxRateBucketBorrowers),
		})
	}
	sort.Slice(d.ActiveByMonthlyRate, func(i, j int) bool {
		if d.ActiveByMonthlyRate[i].Count != d.ActiveByMonthlyRate[j].Count {
			return d.ActiveByMonthlyRate[i].Count > d.ActiveByMonthlyRate[j].Count
		}
		return d.ActiveByMonthlyRate[i].MonthlyRatePct < d.ActiveByMonthlyRate[j].MonthlyRatePct
	})

	for name, bg := range borrowerGroups {
		d.ActiveByBorrower = append(d.ActiveByBorrower, borrowerRow{
			Name: name, ActiveLoans: bg.count,
			PrincipalTotal: money(bg.principal), OutstandingTotal: money(bg.outstanding),
			MonthlyRatesPct: cappedSortedKeys(bg.rates, 8),
		})
	}
	sort.Slice(d.ActiveByBorrower, func(i, j int) bool {
		return rat(d.ActiveByBorrower[i].OutstandingTotal).Cmp(rat(d.ActiveByBorrower[j].OutstandingTotal)) > 0
	})
	if len(d.ActiveByBorrower) > maxBorrowerRows {
		d.ActiveByBorrower = d.ActiveByBorrower[:maxBorrowerRows]
	}
	return d
}

func spendingDigestOf(in digestInput) spendingDigest {
	d := spendingDigest{
		CurrentMonth:        expensePeriod("mês atual", in.expCurrent.TotalAmount, in.expCurrent.EntryCount, in.expCurrent.ByCategory),
		PreviousMonth:       expensePeriod("mês anterior", in.expPrevious.TotalAmount, in.expPrevious.EntryCount, in.expPrevious.ByCategory),
		MonthOverMonthDelta: money(new(big.Rat).Sub(rat(in.expCurrent.TotalAmount), rat(in.expPrevious.TotalAmount))),
		YearToDate:          expensePeriod("ano até agora", in.expYTD.TotalAmount, in.expYTD.EntryCount, in.expYTD.ByCategory),
	}

	subsTotal := new(big.Rat)
	for _, s := range in.subscriptions {
		subsTotal.Add(subsTotal, rat(s.CurrentAmount))
		d.Subscriptions.Items = append(d.Subscriptions.Items, subRow{
			Name: s.Name, Amount: money(rat(s.CurrentAmount)),
			NextChargeEstimate: s.NextEstimatedCharge.Format("2006-01-02"),
		})
	}
	d.Subscriptions.Count = len(in.subscriptions)
	d.Subscriptions.MonthlyTotal = money(subsTotal)

	merchants := map[string]*struct {
		total *big.Rat
		count int
		label string
	}{}
	unreviewed := 0
	for _, e := range in.expenseItems {
		if !e.Reviewed {
			unreviewed++
		}
		if e.Description == nil || strings.TrimSpace(*e.Description) == "" {
			continue
		}
		key := normalise(*e.Description)
		m := merchants[key]
		if m == nil {
			m = &struct {
				total *big.Rat
				count int
				label string
			}{total: new(big.Rat), label: strings.TrimSpace(*e.Description)}
			merchants[key] = m
		}
		m.total.Add(m.total, rat(e.Amount))
		m.count++
	}
	d.UnreviewedCount = unreviewed
	for _, m := range merchants {
		d.TopMerchantsYTD = append(d.TopMerchantsYTD, merchantRow{Description: m.label, Total: money(m.total), Count: m.count})
	}
	sort.Slice(d.TopMerchantsYTD, func(i, j int) bool {
		return rat(d.TopMerchantsYTD[i].Total).Cmp(rat(d.TopMerchantsYTD[j].Total)) > 0
	})
	if len(d.TopMerchantsYTD) > maxTopMerchants {
		d.TopMerchantsYTD = d.TopMerchantsYTD[:maxTopMerchants]
	}
	return d
}

func expensePeriod(label, total string, count int, cats []expenses.CategorySummaryDTO) periodSpending {
	p := periodSpending{Label: label, Total: money(rat(total)), Count: count}
	for _, c := range cats {
		p.ByCategory = append(p.ByCategory, catRow{Category: c.CategoryName, Total: money(rat(c.Total))})
	}
	return p
}

func incomeDigestOf(in digestInput) incomeDigest {
	d := incomeDigest{
		CurrentMonthNet:  money(rat(in.incCurrent.NetTotal)),
		PreviousMonthNet: money(rat(in.incPrevious.NetTotal)),
		YearToDateNet:    money(rat(in.incYTD.NetTotal)),
	}
	for _, c := range in.incCurrent.ByCategory {
		d.CurrentMonthByCategory = append(d.CurrentMonthByCategory, catRow{Category: c.CategoryName, Total: money(rat(c.Total))})
	}
	if in.incCurrent.USDTotal != "" {
		d.ForeignSalary = &foreignSalaryDigest{
			USDReceived:      in.incCurrent.USDTotal,
			BRLTotal:         in.incCurrent.ConvertedBRLTotal,
			EffectiveRateBRL: in.incCurrent.AvgExchangeRate,
		}
	}
	return d
}

func balancesDigestOf(items []finance.BalanceDTO) balancesDigest {
	d := balancesDigest{}
	grand := new(big.Rat)
	byType := map[string]*struct {
		count int
		total *big.Rat
	}{}
	var stalest *finance.BalanceDTO
	for i := range items {
		b := items[i]
		grand.Add(grand, rat(b.CurrentValue))
		t := byType[string(b.Type)]
		if t == nil {
			t = &struct {
				count int
				total *big.Rat
			}{total: new(big.Rat)}
			byType[string(b.Type)] = t
		}
		t.count++
		t.total.Add(t.total, rat(b.CurrentValue))
		if stalest == nil || b.UpdatedOn.Time().Before(stalest.UpdatedOn.Time()) {
			stalest = &items[i]
		}
	}
	d.GrandTotal = money(grand)
	for typ, t := range byType {
		d.ByType = append(d.ByType, balTypeRow{Type: typ, Count: t.count, Total: money(t.total)})
	}
	sort.Slice(d.ByType, func(i, j int) bool {
		return rat(d.ByType[i].Total).Cmp(rat(d.ByType[j].Total)) > 0
	})
	if stalest != nil {
		d.Stalest = &balStale{Name: stalest.Name, Type: string(stalest.Type), UpdatedOn: stalest.UpdatedOn.Time().Format("2006-01-02")}
	}
	return d
}

func goalsDigestOf(items []goals.GoalDTO) []goalRow {
	out := make([]goalRow, 0, len(items))
	for _, g := range items {
		row := goalRow{
			Name: g.Name, Kind: string(g.Kind), Status: string(g.Status),
			TargetAmount: g.TargetAmount, Accumulated: g.Accumulated, Gap: g.Gap,
			ProgressPct: g.ProgressPct, MonthsToTarget: g.MonthsToTarget, OnTrackForDate: g.OnTrackForDate,
		}
		if g.TargetDate != nil {
			row.TargetDate = g.TargetDate.Time().Format("2006-01-02")
		}
		if g.MonthlyContribution != nil {
			row.MonthlyContribution = money(rat(*g.MonthlyContribution))
		}
		out = append(out, row)
	}
	return out
}

func netDigestOf(in digestInput) netDigest {
	return netDigest{
		CurrentMonth:  money(new(big.Rat).Sub(rat(in.incCurrent.NetTotal), rat(in.expCurrent.TotalAmount))),
		PreviousMonth: money(new(big.Rat).Sub(rat(in.incPrevious.NetTotal), rat(in.expPrevious.TotalAmount))),
		YearToDate:    money(new(big.Rat).Sub(rat(in.incYTD.NetTotal), rat(in.expYTD.TotalAmount))),
	}
}

func healthDigestOf(items []health.RecordDTO, personNames map[string]string) healthDigest {
	d := healthDigest{RecordCount: len(items)}
	type key struct{ person, typ string }
	latest := map[key]health.RecordDTO{}
	for _, r := range items {
		if r.AssessmentStatus == nil {
			continue
		}
		as := string(*r.AssessmentStatus)
		if as != "ATTENTION" && as != "ALTERED" {
			continue
		}
		k := key{r.PersonID.String(), string(r.Type)}
		if prev, ok := latest[k]; !ok || r.RecordDate.Time().After(prev.RecordDate.Time()) {
			latest[k] = r
		}
	}
	for _, r := range latest {
		name := personNames[r.PersonID.String()]
		if name == "" {
			name = "(sem nome)"
		}
		f := healthFlag{
			Person: name, Type: string(r.Type), Assessment: string(*r.AssessmentStatus),
			Date: r.RecordDate.Time().Format("2006-01-02"),
		}
		if r.Amount != nil {
			f.Value = *r.Amount
		}
		if r.Unit != nil {
			f.Unit = *r.Unit
		}
		d.NeedsAttention = append(d.NeedsAttention, f)
	}
	sort.Slice(d.NeedsAttention, func(i, j int) bool {
		if d.NeedsAttention[i].Person != d.NeedsAttention[j].Person {
			return d.NeedsAttention[i].Person < d.NeedsAttention[j].Person
		}
		return d.NeedsAttention[i].Type < d.NeedsAttention[j].Type
	})
	if len(d.NeedsAttention) > maxHealthFlags {
		d.NeedsAttention = d.NeedsAttention[:maxHealthFlags]
	}
	return d
}

func unreviewedExpensesOf(items []expenses.ExpenseDTO, cats []categories.CategoryDTO) []unreviewedItem {
	catName := map[string]string{}
	for _, c := range cats {
		catName[c.ID.String()] = c.Name
	}
	out := make([]unreviewedItem, 0)
	for _, e := range items {
		if e.Reviewed {
			continue
		}
		desc := ""
		if e.Description != nil {
			desc = strings.TrimSpace(*e.Description)
		}
		current := ""
		if e.CategoryID != nil {
			current = catName[e.CategoryID.String()]
		}
		out = append(out, unreviewedItem{
			ExpenseID: e.ID.String(), Description: desc, Amount: money(rat(e.Amount)),
			Date: e.ExpenseDate.Time().Format("2006-01-02"), CurrentCategory: current,
		})
		if len(out) == maxUnreviewedItems {
			break
		}
	}
	return out
}

func categoryNames(cats []categories.CategoryDTO) []string {
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		out = append(out, c.Name)
	}
	return out
}

// --- helpers ---

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(s))
	if !ok {
		return new(big.Rat)
	}
	return r
}

func money(r *big.Rat) string { return r.FloatString(2) }

func cappedSortedKeys(set map[string]bool, limit int) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
