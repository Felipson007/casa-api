// Package dashboard composes the other domain modules into the single "everything the dashboard
// home page needs" response — mirrors DashboardResumoService/DashboardService, ported from the
// real Java rather than guessed for the pieces built here. Daily financial/health/organization
// insights are supplied by curator.Service.
package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/assets"
	"github.com/atlas/atlas-go/internal/curator"
	"github.com/atlas/atlas-go/internal/documents"
	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/finance"
	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/personinfo"
	"github.com/atlas/atlas-go/internal/platform/logging"
	"github.com/atlas/atlas-go/internal/platform/money"
	"golang.org/x/sync/errgroup"
)

// recentActivityLimit mirrors LIMITE_ATIVIDADES_RECENTES.
const recentActivityLimit = 4

// Service composes the domain modules' own services — no repository of its own, this package
// never touches SQL directly (matches DashboardResumoService's own role: an orchestrator one layer
// above the modules it reads, not a data owner itself).
type Service struct {
	loans      *loans.Service
	expenses   *expenses.Service
	income     *income.Service
	people     *people.Service
	assets     *assets.Service
	health     *health.Service
	documents  *documents.Service
	finance    *finance.Service
	personinfo *personinfo.Service
	curator    *curator.Service
	// investments adds synced Open Finance positions to net worth. Optional like curator: without it
	// net worth is exactly what it was before investments existed.
	investments InvestmentTotals
}

// InvestmentTotals reports the household's total invested balance (BRL, numeric(15,2) string).
type InvestmentTotals interface {
	TotalInvestments(ctx context.Context) (string, error)
}

// WithInvestments attaches the investments total source and returns s for chaining.
func (s *Service) WithInvestments(i InvestmentTotals) *Service {
	s.investments = i
	return s
}

func NewService(loansSvc *loans.Service, expensesSvc *expenses.Service, incomeSvc *income.Service,
	peopleSvc *people.Service, assetsSvc *assets.Service, healthSvc *health.Service,
	documentsSvc *documents.Service, financeSvc *finance.Service, personinfoSvc *personinfo.Service,
	curatorSvc ...*curator.Service) *Service {
	var dailyCurator *curator.Service
	if len(curatorSvc) > 0 {
		dailyCurator = curatorSvc[0]
	}
	return &Service{loans: loansSvc, expenses: expensesSvc, income: incomeSvc, people: peopleSvc,
		assets: assetsSvc, health: healthSvc, documents: documentsSvc, finance: financeSvc,
		personinfo: personinfoSvc, curator: dailyCurator}
}

// KPIs mirrors DashboardService.calcularKpis (single month).
func (s *Service) KPIs(ctx context.Context, year, month int) (loans.KPIsDTO, error) {
	byMonth, err := s.loans.CalculateKPIs(ctx, []loans.YearMonth{{Year: year, Month: month}})
	if err != nil {
		return loans.KPIsDTO{}, err
	}
	return byMonth[loans.YearMonth{Year: year, Month: month}], nil
}

// KPIsBatch mirrors DashboardService.calcularKpisEmLote — a series of monthsHistory months ending
// at (year, month), including a portfolio score per month (calcularScore).
func (s *Service) KPIsBatch(ctx context.Context, year, month, monthsHistory int) ([]loans.MonthlyKPIsDTO, error) {
	target := loans.YearMonth{Year: year, Month: month}
	months := make([]loans.YearMonth, monthsHistory)
	for i := 0; i < monthsHistory; i++ {
		months[monthsHistory-1-i] = target.AddMonths(-i)
	}

	kpisByMonth, err := s.loans.CalculateKPIs(ctx, months)
	if err != nil {
		return nil, err
	}
	vigentesByMonth, err := s.loans.Vigentes(ctx, months)
	if err != nil {
		return nil, err
	}

	result := make([]loans.MonthlyKPIsDTO, len(months))
	for i, m := range months {
		kpi := kpisByMonth[m]
		score := calculatePortfolioScore(vigentesByMonth[m])
		result[i] = loans.MonthlyKPIsDTO{Year: m.Year, Month: m.Month, KPIs: &kpi, PortfolioScore: score}
	}
	return result, nil
}

// calculatePortfolioScore mirrors DashboardResumoService.calcularScore exactly: 100 minus 70% per
// overdue ratio minus 20% per pending ratio, clamped to [0, 100]. nil when there's nothing to score.
func calculatePortfolioScore(vigentes []loans.LoanSummaryDTO) *int {
	if len(vigentes) == 0 {
		return nil
	}
	var overdue, pending int
	for _, v := range vigentes {
		switch v.Status {
		case loans.MonthStatusOverdue:
			overdue++
		case loans.MonthStatusPending:
			pending++
		}
	}
	total := float64(len(vigentes))
	raw := 100 - (float64(overdue)/total)*70 - (float64(pending)/total)*20
	if raw < 0 {
		raw = 0
	}
	if raw > 100 {
		raw = 100
	}
	score := int(raw + 0.5)
	return &score
}

// PersonDetail aggregates everything the frontend's /pessoas/detalhe page needs in ONE call:
// the person's basic info + health profile + recent health records + their documents + their assets.
// Mirrors PessoaDetalheDto, composed from the Go modules directly (no legacy /api/saude or
// /api/documentos paths that 404'd). Documents are server-filtered by personId (not client-side
// like the old documentosApi.listarPorPessoa, which fetched all rows). HealthProfile implements the
// perfilResumo that was "not yet ported" (see Service.Resumo's doc comment) — now derived here.
func (s *Service) PersonDetail(ctx context.Context, id uuid.UUID) (PersonDetailDTO, error) {
	p, err := s.people.GetPerson(ctx, id)
	if err != nil {
		return PersonDetailDTO{}, err
	}

	dto := PersonDetailDTO{
		ID:        p.ID.String(),
		Name:      p.Name,
		Nickname:  p.Nickname,
		Email:     p.Email,
		BirthDate: p.BirthDate,
		Gender:    string(p.Gender),
		Notes:     p.Notes,
		Active:    p.Active,
		CreatedAt: p.CreatedAt,
	}
	// SelfKinship is a *value* on Person (pointer for nil = unknown legacy relationship); lift it
	// to the DTO as a pointer too so "absent" survives the JSON round-trip (legacy kinship is
	// optional — most graph-derived relationships don't carry it).
	if p.SelfKinship != nil && *p.SelfKinship != "" {
		sk := *p.SelfKinship
		dto.SelfKinship = &sk
	}

	// Health profile + recent records: two calls, both scoped to this person (single round trip
	// each — records is one query filtered by personId in the repo level, not N+1).
	if profile, err := s.health.PerfilSaude(ctx, id); err == nil {
		dto.HealthProfile = &profile
	}
	records, err := s.health.ListRecords(ctx, &id, nil)
	if err != nil {
		return PersonDetailDTO{}, fmt.Errorf("dashboard: listing health records: %w", err)
	}
	dto.HealthRecords = records

	// Documents scoped to this person (server-side filter — fixes the 404 from the frontend
	// sending type=ownerId instead of personId).
	docs, err := s.documents.List(ctx, &id, nil, nil, "")
	if err != nil {
		return PersonDetailDTO{}, fmt.Errorf("dashboard: listing documents: %w", err)
	}
	dto.Documents = docs

	// Assets owned by this person.
	assetsList, err := s.assets.List(ctx, &id, nil, nil)
	if err != nil {
		return PersonDetailDTO{}, fmt.Errorf("dashboard: listing assets: %w", err)
	}
	dto.Assets = assetsList

	// Structured personal/academic/professional facts (personinfo) — folds the module Gustavo asked
	// for directly into this same single call, rather than a separate GET /api/person-information
	// round trip the detail page would otherwise need on top of the six above.
	information, err := s.personinfo.List(ctx, id)
	if err != nil {
		return PersonDetailDTO{}, fmt.Errorf("dashboard: listing person information: %w", err)
	}
	dto.Information = information

	return dto, nil
}

// Resumo mirrors DashboardResumoService.resumo — everything the /dashboard page needs in one call.
// Insights are the latest validated daily snapshot produced by curator.Service. Recommendations
// remains reserved for explicit action proposals rather than duplicating the same insight list.
//
// NetWorth is calculateNetWorth exposed on its own — backs GET /api/dashboard/net-worth for
// callers (the old patrimonioApi.resumo) that only need this piece, not the whole Resumo payload.
func (s *Service) NetWorth(ctx context.Context) (*NetWorthSummaryDTO, error) {
	peopleList, err := s.people.ListPeople(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("dashboard: listing people: %w", err)
	}
	personNames := make(map[string]string, len(peopleList))
	for _, p := range peopleList {
		personNames[p.ID.String()] = p.Name
	}
	return s.calculateNetWorth(ctx, personNames)
}

func (s *Service) Resumo(ctx context.Context, monthsHistory int) (ResumoDTO, error) {
	now := time.Now()
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(4)

	var series []loans.MonthlyKPIsDTO
	group.Go(func() error {
		var err error
		series, err = timed(groupCtx, "kpi_series", func() ([]loans.MonthlyKPIsDTO, error) {
			return s.KPIsBatch(groupCtx, now.Year(), int(now.Month()), monthsHistory)
		})
		return err
	})

	currentMonth := loans.YearMonth{Year: now.Year(), Month: int(now.Month())}
	var vigentesByMonth map[loans.YearMonth][]loans.LoanSummaryDTO
	group.Go(func() error {
		var err error
		vigentesByMonth, err = timed(groupCtx, "current_loans", func() (map[loans.YearMonth][]loans.LoanSummaryDTO, error) {
			return s.loans.Vigentes(groupCtx, []loans.YearMonth{currentMonth})
		})
		return err
	})

	var expensesSummary expenses.MonthlySummaryDTO
	group.Go(func() error {
		var err error
		expensesSummary, err = timed(groupCtx, "expenses_summary", func() (expenses.MonthlySummaryDTO, error) {
			return s.expenses.MonthlySummary(groupCtx, now.Year(), int(now.Month()))
		})
		return err
	})

	var incomeSummary income.MonthlySummaryDTO
	group.Go(func() error {
		var err error
		incomeSummary, err = timed(groupCtx, "income_summary", func() (income.MonthlySummaryDTO, error) {
			return s.income.MonthlySummary(groupCtx, now.Year(), int(now.Month()))
		})
		return err
	})

	var peopleList []people.Person
	group.Go(func() error {
		var err error
		peopleList, err = timed(groupCtx, "people", func() ([]people.Person, error) {
			return s.people.ListPeople(groupCtx, "")
		})
		return err
	})

	var activity []loans.ActivityDTO
	group.Go(func() error {
		var err error
		activity, err = timed(groupCtx, "recent_activity", func() ([]loans.ActivityDTO, error) {
			return s.loans.ListRecentActivity(groupCtx, recentActivityLimit)
		})
		return err
	})

	var insights []AnalysisItemDTO
	group.Go(func() error {
		insights = []AnalysisItemDTO{}
		if s.curator == nil {
			return nil
		}
		items, err := timed(groupCtx, "insights", func() ([]curator.Insight, error) {
			return s.curator.ListInsights(groupCtx)
		})
		if err != nil {
			return err
		}
		insights = make([]AnalysisItemDTO, 0, len(items))
		for _, item := range items {
			insights = append(insights, AnalysisItemDTO{Type: item.Type, Severity: item.Severity, Title: item.Title, Message: item.Message})
		}
		return nil
	})

	if err := group.Wait(); err != nil {
		return ResumoDTO{}, fmt.Errorf("dashboard: loading summary dependencies: %w", err)
	}
	var currentKPIs *loans.KPIsDTO
	if len(series) > 0 {
		currentKPIs = series[len(series)-1].KPIs
	}
	peopleSummaries := make([]PersonSummaryDTO, len(peopleList))
	for i, p := range peopleList {
		peopleSummaries[i] = PersonSummaryDTO{ID: p.ID.String(), Name: p.Name}
	}

	personNames := make(map[string]string, len(peopleList))
	for _, p := range peopleList {
		personNames[p.ID.String()] = p.Name
	}
	netWorth, err := timed(ctx, "net_worth", func() (*NetWorthSummaryDTO, error) {
		return s.calculateNetWorth(ctx, personNames)
	})
	if err != nil {
		return ResumoDTO{}, fmt.Errorf("dashboard: calculating net worth: %w", err)
	}

	currentLoans := vigentesByMonth[currentMonth]
	if currentLoans == nil {
		currentLoans = []loans.LoanSummaryDTO{}
	}

	return ResumoDTO{
		CurrentKPIs:     currentKPIs,
		KPISeries:       series,
		CurrentLoans:    currentLoans,
		ExpensesSummary: expensesSummary,
		IncomeSummary:   incomeSummary,
		People:          peopleSummaries,
		RecentActivity:  activity,
		Insights:        insights,
		Recommendations: []AnalysisItemDTO{},
		NetWorth:        netWorth,
	}, nil
}

func timed[T any](ctx context.Context, dependency string, fn func() (T, error)) (T, error) {
	start := time.Now()
	value, err := fn()
	logging.FromContext(ctx).Info("dashboard dependency", "dependency", dependency, "duration_ms", time.Since(start).Milliseconds(), "error", err != nil)
	return value, err
}

// calculateNetWorth mirrors PatrimonioService.resumo: assets (using CurrentEstimatedValue when
// set, falling back to AcquisitionValue — mirrors the old Java's own fallback, see BemDto's doc
// comment) + financial balances + active loans' outstanding balance (money owed TO the household),
// summed for the net worth total and broken down by category/person.
func (s *Service) calculateNetWorth(ctx context.Context, personNames map[string]string) (*NetWorthSummaryDTO, error) {
	assetList, err := s.assets.List(ctx, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("listing assets: %w", err)
	}
	balances, err := s.finance.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing financial balances: %w", err)
	}
	activeLoans, err := s.loans.List(ctx, nil, "")
	if err != nil {
		return nil, fmt.Errorf("listing loans: %w", err)
	}

	totalAssets := money.Zero
	activeAssetCount, assetsWithDebt := 0, 0
	byPerson := make(map[string]string)
	for _, a := range assetList {
		if a.Status == assets.StatusActive {
			activeAssetCount++
		}
		if a.HasDebt {
			assetsWithDebt++
		}
		value := a.AcquisitionValue
		if a.CurrentEstimatedValue != nil {
			value = a.CurrentEstimatedValue
		}
		if value == nil {
			continue
		}
		var err error
		totalAssets, err = money.Add(totalAssets, *value)
		if err != nil {
			return nil, err
		}
		personKey := "unassigned"
		if a.PersonID != nil {
			personKey = a.PersonID.String()
		}
		sum, err := money.Add(orZero(byPerson[personKey]), *value)
		if err != nil {
			return nil, err
		}
		byPerson[personKey] = sum
	}

	totalBalances := money.Zero
	for _, b := range balances {
		var err error
		totalBalances, err = money.Add(totalBalances, b.CurrentValue)
		if err != nil {
			return nil, err
		}
	}

	totalReceivable := money.Zero
	for _, l := range activeLoans {
		if l.Status != loans.StatusActive {
			continue
		}
		var err error
		totalReceivable, err = money.Add(totalReceivable, l.OutstandingBalance)
		if err != nil {
			return nil, err
		}
	}

	totalInvestments := money.Zero
	if s.investments != nil {
		totalInvestments, err = s.investments.TotalInvestments(ctx)
		if err != nil {
			return nil, fmt.Errorf("summing investments: %w", err)
		}
	}

	netWorth, err := money.Add(totalAssets, totalBalances)
	if err != nil {
		return nil, err
	}
	netWorth, err = money.Add(netWorth, totalInvestments)
	if err != nil {
		return nil, err
	}
	netWorth, err = money.Add(netWorth, totalReceivable)
	if err != nil {
		return nil, err
	}

	distribution := []NetWorthDistributionItemDTO{
		{Category: "assets", Value: totalAssets},
		{Category: "financial_balances", Value: totalBalances},
		{Category: "loans_receivable", Value: totalReceivable},
	}
	if totalInvestments != money.Zero {
		distribution = append(distribution, NetWorthDistributionItemDTO{Category: "investments", Value: totalInvestments})
	}
	byPersonList := make([]NetWorthByPersonDTO, 0, len(byPerson))
	for key, value := range byPerson {
		var personID *string
		name := "Não atribuído"
		if key != "unassigned" {
			id := key
			personID = &id
			if resolved, ok := personNames[key]; ok {
				name = resolved
			}
		}
		byPersonList = append(byPersonList, NetWorthByPersonDTO{PersonID: personID, PersonName: name, Value: value})
	}

	return &NetWorthSummaryDTO{
		TotalAssets:              totalAssets,
		TotalFinancialBalances:   totalBalances,
		TotalReceivableFromLoans: totalReceivable,
		TotalInvestments:         totalInvestments,
		NetWorth:                 netWorth,
		TotalActiveAssets:        activeAssetCount,
		TotalAssetsWithDebt:      assetsWithDebt,
		Distribution:             distribution,
		ByPerson:                 byPersonList,
	}, nil
}

func orZero(s string) string {
	if s == "" {
		return money.Zero
	}
	return s
}
