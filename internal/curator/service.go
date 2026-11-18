package curator

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/categories"
	"github.com/atlas/atlas-go/internal/clients"
	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/finance"
	"github.com/atlas/atlas-go/internal/goals"
	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/pandora"
	"github.com/atlas/atlas-go/internal/people"
)

var technicalIDPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{8}(?:-[0-9a-f-]{9,})?\b`)

type Service struct {
	repo       Repository
	pandora    *pandora.Service
	expenses   *expenses.Service
	income     *income.Service
	categories *categories.Service
	loans      *loans.Service
	clients    *clients.Service
	health     *health.Service
	finance    *finance.Service
	people     *people.Service
	goals      *goals.Service
}

func NewService(repo Repository, pandoraSvc *pandora.Service, expensesSvc *expenses.Service,
	incomeSvc *income.Service, categoriesSvc *categories.Service, loansSvc *loans.Service,
	clientsSvc *clients.Service, healthSvc *health.Service, financeSvc *finance.Service,
	peopleSvc *people.Service, goalsSvc *goals.Service) *Service {
	return &Service{repo: repo, pandora: pandoraSvc, expenses: expensesSvc, income: incomeSvc,
		categories: categoriesSvc, loans: loansSvc, clients: clientsSvc, health: healthSvc,
		finance: financeSvc, people: peopleSvc, goals: goalsSvc}
}

func (s *Service) ListInsights(ctx context.Context) ([]Insight, error) {
	return s.repo.ListInsights(ctx)
}

// RunDaily performs one bounded review for the supplied civil day. It can only change the category
// of an unreviewed expense, and only to an existing expense category. All source values remain
// immutable; insights are replaced atomically only after a successful model response.
func (s *Service) RunDaily(ctx context.Context, date time.Time) (Report, error) {
	done, err := s.repo.WasCompleted(ctx, date)
	if err != nil {
		return Report{}, err
	}
	if done {
		return Report{Skipped: true}, nil
	}

	expenseItems, err := s.expenses.List(ctx, nil, nil, "")
	if err != nil {
		return Report{}, err
	}
	t := categories.TypeExpense
	categoryItems, err := s.categories.List(ctx, &t)
	if err != nil {
		return Report{}, err
	}
	loanItems, err := s.loans.List(ctx, nil, "")
	if err != nil {
		return Report{}, err
	}
	clientItems, err := s.clients.List(ctx, nil, "")
	if err != nil {
		return Report{}, err
	}
	healthItems, err := s.health.ListRecords(ctx, nil, nil)
	if err != nil {
		return Report{}, err
	}
	peopleItems, err := s.people.ListPeople(ctx, "")
	if err != nil {
		return Report{}, err
	}
	balances, err := s.finance.List(ctx)
	if err != nil {
		return Report{}, err
	}
	goalItems, err := s.goals.List(ctx)
	if err != nil {
		return Report{}, err
	}

	// Keep the imported-invoice Recurring flags current before the subscription rollup reads them.
	s.markRepeatedInvoiceExpensesRecurring(ctx, expenseItems)

	year, month := date.Year(), int(date.Month())
	prevYear, prevMonth := year, month-1
	if prevMonth == 0 {
		prevYear, prevMonth = year-1, 12
	}
	expCurrent, err := s.expenses.MonthlySummary(ctx, year, month)
	if err != nil {
		return Report{}, err
	}
	expPrevious, err := s.expenses.MonthlySummary(ctx, prevYear, prevMonth)
	if err != nil {
		return Report{}, err
	}
	expYTD, err := s.expenses.AnnualSummary(ctx, year)
	if err != nil {
		return Report{}, err
	}
	subscriptions, err := s.expenses.Subscriptions(ctx, date)
	if err != nil {
		return Report{}, err
	}
	incCurrent, err := s.income.MonthlySummary(ctx, year, month)
	if err != nil {
		return Report{}, err
	}
	incPrevious, err := s.income.MonthlySummary(ctx, prevYear, prevMonth)
	if err != nil {
		return Report{}, err
	}
	incYTD, err := s.income.AnnualSummary(ctx, year)
	if err != nil {
		return Report{}, err
	}

	clientNames := make(map[string]string, len(clientItems))
	for _, c := range clientItems {
		clientNames[c.ID.String()] = c.Name
	}
	personNames := make(map[string]string, len(peopleItems))
	for _, p := range peopleItems {
		personNames[p.ID.String()] = p.Name
	}

	digest := buildDigest(digestInput{
		date:          date.Format("2006-01-02"),
		loans:         loanItems,
		clientNames:   clientNames,
		expenseItems:  expenseItems,
		expCurrent:    expCurrent,
		expPrevious:   expPrevious,
		expYTD:        expYTD,
		subscriptions: subscriptions,
		incCurrent:    incCurrent,
		incPrevious:   incPrevious,
		incYTD:        incYTD,
		balances:      balances,
		goals:         goalItems,
		healthItems:   healthItems,
		personNames:   personNames,
		expenseCats:   categoryItems,
	})
	encoded, err := json.Marshal(digest)
	if err != nil {
		return Report{}, err
	}

	response, err := s.pandora.Chat(ctx, "",
		"Você é um consultor financeiro e de organização pessoal sênior analisando os dados consolidados de uma pessoa. "+
			"Aja como especialista: aponte o que realmente merece atenção — riscos, oportunidades, padrões e inconsistências —, "+
			"sempre com base nos números fornecidos. Todos os totais, médias e rankings relevantes já vêm calculados no JSON: "+
			"use-os, não recalcule e não invente dados. Não faça diagnóstico médico. Escreva em português, direto ao ponto, "+
			"sem jargão e sem identificadores técnicos.",
		`O JSON abaixo traz os dados consolidados de hoje.

Tarefa 1 (mecânica): para cada item em "unreviewedExpenses" cuja categoria atual esteja claramente errada, proponha a correta usando exatamente um nome de "expenseCategories". Nunca altere valor, data ou descrição. Se nada estiver claramente errado, deixe "reclassifications" vazio.

Tarefa 2: gere os insights que um especialista destacaria a partir destes dados — apenas os genuinamente relevantes, sem número mínimo nem máximo. Cada insight tem "type" (FINANCIAL = finanças em geral, incluindo metas de planejamento; LOANS = a carteira de empréstimos que a pessoa concede; HEALTH = saúde; ORGANIZATION = organização/consistência dos dados), "severity" (INFO, WARNING ou CRITICAL), "title" curto e "message" objetiva. Se "income.foreignSalaryCurrentMonth" estiver presente, a pessoa recebe salário em USD de um empregador estrangeiro (convertido mensalmente, ex.: via Wise) — considere gerar um insight FINANCIAL informando o valor recebido em USD, o total convertido em BRL e a taxa efetiva do mês, sinalizando se a taxa variou de forma notável frente aos meses anteriores.

Retorne SOMENTE JSON: {"reclassifications":[{"expenseId":"...","category":"..."}],"insights":[{"type":"...","severity":"...","title":"...","message":"..."}]}

Dados:
`+string(encoded))
	if err != nil {
		return Report{}, err
	}
	var result modelResult
	if err := parseModelJSON(response, &result); err != nil {
		return Report{}, err
	}

	reclassified := s.applyReclassifications(ctx, expenseItems, categoryItems, result.Reclassifications)
	insights := sanitiseInsights(result.Insights)
	if err := s.repo.SaveSnapshot(ctx, date, insights, reclassified); err != nil {
		return Report{}, err
	}
	return Report{Reclassified: reclassified, Insights: len(insights)}, nil
}

type modelResult struct {
	Reclassifications []struct {
		ExpenseID string `json:"expenseId"`
		Category  string `json:"category"`
	} `json:"reclassifications"`
	Insights []Insight `json:"insights"`
}

func (s *Service) applyReclassifications(ctx context.Context, expenseItems []expenses.ExpenseDTO,
	categoryItems []categories.CategoryDTO, proposals []struct {
		ExpenseID string `json:"expenseId"`
		Category  string `json:"category"`
	}) int {
	byID := make(map[uuid.UUID]expenses.ExpenseDTO, len(expenseItems))
	for _, item := range expenseItems {
		if !item.Reviewed {
			byID[item.ID] = item
		}
	}
	categoryByName := make(map[string]uuid.UUID, len(categoryItems))
	for _, item := range categoryItems {
		categoryByName[normalise(item.Name)] = item.ID
	}
	updated := 0
	seen := map[uuid.UUID]bool{}
	for _, proposal := range proposals {
		id, err := uuid.Parse(proposal.ExpenseID)
		if err != nil || seen[id] {
			continue
		}
		item, ok := byID[id]
		categoryID, categoryOK := categoryByName[normalise(proposal.Category)]
		if !ok || !categoryOK || (item.CategoryID != nil && *item.CategoryID == categoryID) {
			continue
		}
		_, err = s.expenses.Update(ctx, id, expenses.UpdateExpenseRequest{
			DocumentID: item.DocumentID, CategoryID: &categoryID, PersonID: item.PersonID,
			Amount: item.Amount, ExpenseDate: item.ExpenseDate, Description: item.Description,
			Reviewed: item.Reviewed, OriginalDate: item.OriginalDate, Recurring: item.Recurring,
		})
		if err == nil {
			updated++
			seen[id] = true
		}
	}
	return updated
}

const (
	// A description has to repeat across at least this many competence months before it can be a
	// subscription. Two was the old threshold and it was far too loose — every habitual merchant
	// clears it, and a single invoice import can even put two months of one merchant in view at
	// once.
	minRecurringMonths = 3
	// And it has to bill about once a month. A subscription charges on a cycle; a merchant you
	// simply use a lot charges whenever you use it. The slack absorbs a proration landing beside
	// a regular charge. Without this, "Uber — 101 charges in August" was a subscription.
	maxChargesPerMonthSlack = 1
	// Amounts have to hold roughly steady. Subscriptions do get more expensive — that is what the
	// price-increase badge is for — so the dearest charge may be half again the cheapest, which
	// still rejects the spread of ordinary ride/delivery spending.
	maxRecurringAmountRatio = 1.5
)

// markRepeatedInvoiceExpensesRecurring maintains the Recurring flag on imported invoice lines: it
// promotes descriptions that behave like a subscription and demotes those that stop behaving like
// one. It never touches manually entered expenses (DocumentID == nil), whose Recurring flag is the
// user's own statement.
//
// "Behaves like a subscription" means what a person means by it: the same amount, about once a
// month, over several months. The old rule — the description appears in two distinct months — is
// true of every merchant anyone uses regularly, and put Uber (101 charges in one month), a
// two-charge iFood order and a two-charge game-store purchase on the subscriptions screen. The
// demotion half matters just as much: those rows are already flagged in the database, so a rule
// change that only ever adds flags would leave them there forever.
func (s *Service) markRepeatedInvoiceExpensesRecurring(ctx context.Context, items []expenses.ExpenseDTO) {
	type group struct {
		months map[string]bool
		count  int
		min    *big.Rat
		max    *big.Rat
	}
	groups := map[string]*group{}
	for _, item := range items {
		if item.DocumentID == nil || item.Description == nil || strings.TrimSpace(*item.Description) == "" {
			continue
		}
		key := normalise(*item.Description)
		g := groups[key]
		if g == nil {
			g = &group{months: map[string]bool{}}
			groups[key] = g
		}
		g.months[item.ExpenseDate.Time().Format("2006-01")] = true
		g.count++
		if amount, ok := new(big.Rat).SetString(item.Amount); ok && amount.Sign() > 0 {
			if g.min == nil || amount.Cmp(g.min) < 0 {
				g.min = amount
			}
			if g.max == nil || amount.Cmp(g.max) > 0 {
				g.max = amount
			}
		}
	}

	qualifies := func(g *group) bool {
		if g == nil || len(g.months) < minRecurringMonths {
			return false
		}
		if g.count > len(g.months)+maxChargesPerMonthSlack {
			return false
		}
		if g.min == nil || g.max == nil || g.min.Sign() <= 0 {
			return false
		}
		limit := new(big.Rat).Mul(g.min, new(big.Rat).SetFloat64(maxRecurringAmountRatio))
		return g.max.Cmp(limit) <= 0
	}

	for _, item := range items {
		if item.DocumentID == nil || item.Description == nil {
			continue
		}
		want := qualifies(groups[normalise(*item.Description)])
		if item.Recurring == want {
			continue
		}
		_, _ = s.expenses.Update(ctx, item.ID, expenses.UpdateExpenseRequest{
			DocumentID: item.DocumentID, CategoryID: item.CategoryID, PersonID: item.PersonID,
			Amount: item.Amount, ExpenseDate: item.ExpenseDate, Description: item.Description,
			Reviewed: item.Reviewed, OriginalDate: item.OriginalDate, Recurring: want,
		})
	}
}

func sanitiseInsights(items []Insight) []Insight {
	result := make([]Insight, 0, len(items))
	for _, item := range items {
		item.Type = strings.ToUpper(strings.TrimSpace(item.Type))
		item.Severity = strings.ToUpper(strings.TrimSpace(item.Severity))
		item.Title = strings.TrimSpace(item.Title)
		item.Message = strings.TrimSpace(item.Message)
		if item.Type != "FINANCIAL" && item.Type != "LOANS" && item.Type != "HEALTH" && item.Type != "ORGANIZATION" {
			continue
		}
		if item.Severity != "INFO" && item.Severity != "WARNING" && item.Severity != "CRITICAL" {
			continue
		}
		if item.Title == "" || item.Message == "" {
			continue
		}
		if technicalIDPattern.MatchString(item.Title) || technicalIDPattern.MatchString(item.Message) {
			continue
		}
		if len([]rune(item.Title)) > 160 {
			item.Title = string([]rune(item.Title)[:160])
		}
		result = append(result, item)
		if len(result) == 8 {
			break
		}
	}
	return result
}

func parseModelJSON(value string, target any) error {
	start, end := strings.Index(value, "{"), strings.LastIndex(value, "}")
	if start < 0 || end < start {
		return fmt.Errorf("curator: model response contains no JSON object")
	}
	if err := json.Unmarshal([]byte(value[start:end+1]), target); err != nil {
		return fmt.Errorf("curator: invalid model JSON: %w", err)
	}
	return nil
}

func normalise(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
