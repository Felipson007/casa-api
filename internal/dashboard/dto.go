package dashboard

import (
	"time"

	"github.com/atlas/atlas-go/internal/assets"
	"github.com/atlas/atlas-go/internal/documents"
	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/personinfo"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// NetWorthDistributionItemDTO mirrors PatrimonioDistribuicaoItemDto.
type NetWorthDistributionItemDTO struct {
	Category string `json:"category"`
	Value    string `json:"value"`
}

// NetWorthByPersonDTO mirrors PatrimonioPessoaDto.
type NetWorthByPersonDTO struct {
	PersonID   *string `json:"personId"`
	PersonName string  `json:"personName"`
	Value      string  `json:"value"`
}

// NetWorthSummaryDTO mirrors PatrimonioResumoDto — a household-wide net worth snapshot: assets +
// financial balances + what's owed back on active loans, all summed. Distribution/by-person
// breakdowns are computed from the same already-fetched asset/balance lists (no extra round trips).
type NetWorthSummaryDTO struct {
	TotalAssets              string                        `json:"totalAssets"`
	TotalFinancialBalances   string                        `json:"totalFinancialBalances"`
	TotalReceivableFromLoans string                        `json:"totalReceivableFromLoans"`
	TotalInvestments         string                        `json:"totalInvestments"`
	NetWorth                 string                        `json:"netWorth"`
	TotalActiveAssets        int                           `json:"totalActiveAssets"`
	TotalAssetsWithDebt      int                           `json:"totalAssetsWithDebt"`
	Distribution             []NetWorthDistributionItemDTO `json:"distribution"`
	ByPerson                 []NetWorthByPersonDTO         `json:"byPerson"`
}

// ResumoDTO mirrors DashboardResumoDto — everything the dashboard home page needs in one response.
// Insights contains the latest daily curator snapshot; recommendations remains reserved for
// explicit actions that can be confirmed by the user.
type ResumoDTO struct {
	CurrentKPIs     *loans.KPIsDTO             `json:"currentKpis"`
	KPISeries       []loans.MonthlyKPIsDTO     `json:"kpiSeries"`
	CurrentLoans    []loans.LoanSummaryDTO     `json:"currentLoans"`
	ExpensesSummary expenses.MonthlySummaryDTO `json:"expensesSummary"`
	IncomeSummary   income.MonthlySummaryDTO   `json:"incomeSummary"`
	People          []PersonSummaryDTO         `json:"people"`
	RecentActivity  []loans.ActivityDTO        `json:"recentActivity"`
	Insights        []AnalysisItemDTO          `json:"insights"`
	Recommendations []AnalysisItemDTO          `json:"recommendations"`
	NetWorth        *NetWorthSummaryDTO        `json:"netWorth"`
}

// PersonSummaryDTO is a minimal per-person entry for the dashboard (no health profile — see
// ResumoDTO's doc comment).
type PersonSummaryDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PersonDetailDTO is the single-call aggregate the frontend's pessoa detalhe page needs: it pairs
// the person's basic info with their health profile + recent records + their documents + their
// assets + their structured personal/academic/professional facts (personinfo), so the UI makes ONE
// request instead of seven separate /api/people / /api/health / /api/documents / /api/assets /
// /api/person-information calls (several of which were 404'ing because the frontend still used the
// old Java paths). Mirrors PessoaDetalheDto from the old Spring app, ported to read from the Go
// modules directly.
type PersonDetailDTO struct {
	ID            string                      `json:"id"`
	Name          string                      `json:"name"`
	Nickname      *string                     `json:"nickname,omitempty"`
	Email         *string                     `json:"email,omitempty"`
	BirthDate     *civildate.Date             `json:"birthDate,omitempty"`
	Gender        string                      `json:"gender,omitempty"`
	Notes         *string                     `json:"notes,omitempty"`
	Active        bool                        `json:"active"`
	SelfKinship   *people.LegacyKinship       `json:"selfKinship,omitempty"`
	CreatedAt     time.Time                   `json:"createdAt"`
	HealthProfile *health.HealthProfileDTO    `json:"healthProfile,omitempty"`
	HealthRecords []health.RecordDTO          `json:"healthRecords,omitempty"`
	Documents     []documents.DocumentDTO     `json:"documents,omitempty"`
	Assets        []assets.AssetDTO           `json:"assets,omitempty"`
	Information   []personinfo.InformationDTO `json:"information,omitempty"`
}

// AnalysisItemDTO mirrors AnaliseItemDto and is populated from the daily curator snapshot.
type AnalysisItemDTO struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Message  string `json:"message"`
}
