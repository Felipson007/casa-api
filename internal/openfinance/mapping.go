package openfinance

import "strings"

// Category targets. Pluggy's categories (English, or Portuguese depending on the institution) are
// folded into this short list; a category missing from the household's own list is created on
// first use, and anything unmapped stays uncategorised (nil) with reviewed=false, so the existing
// review flow surfaces it instead of a wrong guess hiding in a total.
const (
	CatFood          = "Alimentação"
	CatTransport     = "Transporte"
	CatHousing       = "Moradia"
	CatHealth        = "Saúde"
	CatEducation     = "Educação"
	CatLeisure       = "Lazer"
	CatShopping      = "Compras"
	CatSubscriptions = "Assinaturas e serviços"
	CatTaxesFees     = "Impostos e taxas"
	CatTravel        = "Viagens"
	CatCardInterest  = "Juros e encargos"
	CatAdjustments   = "Ajustes e Estornos" // same name the PDF reconciliation flow uses

	CatSalary    = "Salário"
	CatYield     = "Rendimentos"
	CatRefunds   = "Reembolsos"
	CatOtherInc  = "Outros"
	catNoMapping = ""
)

type categoryRule struct {
	needles []string // matched against the normalised provider category, then the description
	target  string
}

// Order is priority: the first rule with a matching needle wins. More specific first.
var expenseCategoryRules = []categoryRule{
	{[]string{"travel", "viagem", "airline", "aereo", "hotel", "lodging", "hospedagem", "accomodation", "accommodation"}, CatTravel},
	{[]string{"grocer", "supermercado", "mercado", "eating out", "restaurant", "restaurante", "food", "alimenta", "padaria", "bakery", "delivery", "lanchonete", "cafe"}, CatFood},
	{[]string{"transport", "ride", "taxi", "uber", "gas station", "combustivel", "posto", "fuel", "parking", "estacionamento", "pedagio", "toll", "public transport", "onibus", "metro", "automotive", "automotivo", "vehicle"}, CatTransport},
	{[]string{"health", "saude", "pharmac", "farmacia", "drogaria", "medic", "hospital", "dentist", "odont", "clinic", "clinica", "wellness", "gym", "academia"}, CatHealth},
	{[]string{"educa", "school", "escola", "faculdade", "university", "course", "curso", "book", "livraria"}, CatEducation},
	{[]string{"rent", "aluguel", "housing", "moradia", "condominio", "utilities", "energy", "energia", "electricity", "water", "agua", "gas ", "home", "casa", "houseware"}, CatHousing},
	{[]string{"streaming", "subscription", "assinatura", "digital service", "software", "telecom", "internet", "phone", "telefone", "netflix", "spotify", "prime video", "disney", "youtube"}, CatSubscriptions},
	{[]string{"tax", "imposto", "iof", "fee", "tarifa", "taxa", "insurance", "seguro", "bank charge", "anuidade"}, CatTaxesFees},
	{[]string{"entertainment", "lazer", "leisure", "cinema", "game", "jogo", "bar ", "nightlife", "show", "sport", "stadium", "arena", "ticket"}, CatLeisure},
	{[]string{"shopping", "compras", "clothing", "vestuario", "electronics", "eletronic", "department store", "marketplace", "retail", "varejo", "kids", "toys", "office supplies", "pet supplies", "vet"}, CatShopping},
	{[]string{"interest", "juros", "finance charge", "encargo", "multa"}, CatCardInterest},
}

var incomeCategoryRules = []categoryRule{
	{[]string{"salary", "salario", "payroll", "folha", "wage", "proventos", "pro labore"}, CatSalary},
	{[]string{"interest", "juros", "dividend", "dividendo", "yield", "rendimento", "cashback"}, CatYield},
	{[]string{"refund", "estorno", "reembolso", "reimbursement", "devolucao"}, CatRefunds},
}

// MapExpenseCategory picks the household-facing category name for a transaction, or "" when
// nothing matches confidently.
func MapExpenseCategory(providerCategory, description string) string {
	return mapCategory(expenseCategoryRules, providerCategory, description)
}

// MapIncomeCategory is MapExpenseCategory for credits.
func MapIncomeCategory(providerCategory, description string) string {
	return mapCategory(incomeCategoryRules, providerCategory, description)
}

func mapCategory(rules []categoryRule, providerCategory, description string) string {
	cat, desc := normalize(providerCategory), normalize(description)
	for _, text := range []string{cat, desc} {
		if strings.TrimSpace(text) == "" {
			continue
		}
		for _, r := range rules {
			if hasAny(text, r.needles...) {
				return r.target
			}
		}
	}
	return catNoMapping
}

// BalanceTypeFor maps a Pluggy account subtype to financial_balances.balance_type.
func BalanceTypeFor(subtype string) string {
	if hasAny(normalize(subtype), "saving", "poupanca") {
		return "SAVINGS"
	}
	return "CHECKING"
}
