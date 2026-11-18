package openfinance

import (
	"math/big"
	"testing"
	"time"

	"github.com/atlas/atlas-go/internal/pluggy"
)

func d(s string) pluggy.Decimal { return pluggy.Decimal(s) }

func rat(s string) *big.Rat { r, _ := new(big.Rat).SetString(s); return r }

func day(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		in   ClassifyInput
		want ProjectionKind
	}{
		{"pending is never counted", ClassifyInput{AccountType: "BANK", Direction: "DEBIT", Amount: d("-10"), Status: "PENDING", Description: "PADARIA"}, KindPending},
		{"pending card charge on the open statement is spending", ClassifyInput{AccountType: "CREDIT", Direction: "DEBIT", Amount: d("20.92"), Status: "PENDING", Description: "Uber UBER *TRIP HELP.US"}, KindExpense},
		{"card charge is spending", ClassifyInput{AccountType: "CREDIT", Direction: "DEBIT", Amount: d("89.90"), Status: "POSTED", Description: "UBER *TRIP"}, KindExpense},
		{"card statement payment (credit side) is ignored", ClassifyInput{AccountType: "CREDIT", Direction: "CREDIT", Amount: d("-1500"), Status: "POSTED", Description: "PAGAMENTO RECEBIDO"}, KindIgnoredCardPayment},
		{"card statement payment sent positive is still ignored", ClassifyInput{AccountType: "CREDIT", Direction: "CREDIT", Amount: d("8371.26"), Status: "POSTED", Description: "Pagamento recebido"}, KindIgnoredCardPayment},
		{"card charge mentioning pagamento is spending", ClassifyInput{AccountType: "CREDIT", Direction: "DEBIT", Amount: d("79.73"), Status: "POSTED", Description: "JUROS PAGAMENTO CONTAS"}, KindExpense},
		{"card refund is a negative expense, not ignored", ClassifyInput{AccountType: "CREDIT", Direction: "CREDIT", Amount: d("-35.50"), Status: "POSTED", Description: "ESTORNO AMAZON"}, KindExpense},
		{"bank debit paying the card is ignored", ClassifyInput{AccountType: "BANK", Direction: "DEBIT", Amount: d("-1500"), Status: "POSTED", Description: "PAGAMENTO FATURA CARTAO ITAU"}, KindIgnoredCardPayment},
		{"bank debit by provider category", ClassifyInput{AccountType: "BANK", Direction: "DEBIT", Amount: d("-800"), Status: "POSTED", Description: "PGTO", ProviderCategory: "Credit card payment"}, KindIgnoredCardPayment},
		{"utility fatura is real spending", ClassifyInput{AccountType: "BANK", Direction: "DEBIT", Amount: d("-230"), Status: "POSTED", Description: "PAGAMENTO DE FATURA ENERGIA ENEL"}, KindExpense},
		{"own transfer by category", ClassifyInput{AccountType: "BANK", Direction: "DEBIT", Amount: d("-500"), Status: "POSTED", Description: "PIX ENVIADO", ProviderCategory: "Same person transfer - PIX"}, KindIgnoredTransfer},
		{"investment application", ClassifyInput{AccountType: "BANK", Direction: "DEBIT", Amount: d("-1000"), Status: "POSTED", Description: "APLICACAO CDB"}, KindIgnoredInvestment},
		{"investment redemption is not income", ClassifyInput{AccountType: "BANK", Direction: "CREDIT", Amount: d("1000"), Status: "POSTED", Description: "RESGATE CDB"}, KindIgnoredInvestment},
		{"salary is income", ClassifyInput{AccountType: "BANK", Direction: "CREDIT", Amount: d("8000"), Status: "POSTED", Description: "SALARIO EMPRESA"}, KindIncome},
		{"pix to a third party is spending", ClassifyInput{AccountType: "BANK", Direction: "DEBIT", Amount: d("-120"), Status: "POSTED", Description: "PIX ENVIADO JOAO", ProviderCategory: "Transfer - PIX"}, KindExpense},
		{"plain debit", ClassifyInput{AccountType: "BANK", Direction: "DEBIT", Amount: d("-42.10"), Status: "POSTED", Description: "Padaria do Zé"}, KindExpense},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.in); got != c.want {
				t.Errorf("Classify = %s, want %s", got, c.want)
			}
		})
	}
}

// The invariant that motivates the whole module: a purchase on the card and the bank debit that
// pays that card's statement must NOT both be spending.
func TestNoDoubleCount_CardPurchaseAndItsPayment(t *testing.T) {
	purchase := Classify(ClassifyInput{AccountType: "CREDIT", Amount: d("300"), Status: "POSTED", Description: "LOJA X"})
	payment := Classify(ClassifyInput{AccountType: "BANK", Direction: "DEBIT", Amount: d("-300"), Status: "POSTED", Description: "PAGAMENTO DE FATURA ITAUCARD"})
	spending := 0
	for _, k := range []ProjectionKind{purchase, payment} {
		if k == KindExpense {
			spending++
		}
	}
	if spending != 1 {
		t.Fatalf("expected exactly one of {purchase, payment} to count as spending, got %d (%s, %s)", spending, purchase, payment)
	}
}

func TestBillCompetence(t *testing.T) {
	bill := pluggy.Bill{DueDate: "2026-09-05T00:00:00.000Z"}
	if got := BillCompetence(bill, []string{"2026-08-02", "2026-08-27T10:00:00Z"}); got != "2026-08-01" {
		t.Errorf("with purchases: %s", got)
	}
	if got := BillCompetence(bill, nil); got != "2026-08-01" { // 09-05 minus 10d = 08-26
		t.Errorf("fallback: %s", got)
	}
	if got := BillCompetence(pluggy.Bill{}, nil); got != "" {
		t.Errorf("no data must yield empty, got %s", got)
	}
}

func TestBillFinanceCharges_Exact(t *testing.T) {
	b := pluggy.Bill{FinanceCharges: []pluggy.FinanceCharge{{Amount: d("0.10")}, {Amount: d("0.20")}, {Amount: d("12.34")}}}
	if got := BillFinanceCharges(b); got != "12.64" {
		t.Errorf("got %s", got)
	}
}

func TestPairTransfers(t *testing.T) {
	cands := []Candidate{
		{ID: "out1", AccountID: "A", Date: day("2026-08-10"), Amount: rat("500"), Direction: "DEBIT"},
		{ID: "in1", AccountID: "B", Date: day("2026-08-10"), Amount: rat("500"), Direction: "CREDIT"},
		// same account on both sides: never a transfer between two accounts
		{ID: "out2", AccountID: "A", Date: day("2026-08-12"), Amount: rat("70"), Direction: "DEBIT"},
		{ID: "in2", AccountID: "A", Date: day("2026-08-12"), Amount: rat("70"), Direction: "CREDIT"},
		// too far apart
		{ID: "out3", AccountID: "A", Date: day("2026-08-01"), Amount: rat("90"), Direction: "DEBIT"},
		{ID: "in3", AccountID: "B", Date: day("2026-08-05"), Amount: rat("90"), Direction: "CREDIT"},
		// one credit can only pair once
		{ID: "out4", AccountID: "A", Date: day("2026-08-20"), Amount: rat("10"), Direction: "DEBIT"},
		{ID: "out5", AccountID: "A", Date: day("2026-08-20"), Amount: rat("10"), Direction: "DEBIT"},
		{ID: "in4", AccountID: "B", Date: day("2026-08-20"), Amount: rat("10"), Direction: "CREDIT"},
	}
	got := map[string]bool{}
	for _, id := range PairTransfers(cands) {
		got[id] = true
	}
	if !got["out1"] || !got["in1"] {
		t.Errorf("out1/in1 must pair: %v", got)
	}
	for _, id := range []string{"out2", "in2", "out3", "in3"} {
		if got[id] {
			t.Errorf("%s must not pair", id)
		}
	}
	if !got["in4"] || got["out4"] == got["out5"] {
		t.Errorf("exactly one of out4/out5 pairs with in4: %v", got)
	}
}

func TestMatchBillPayments(t *testing.T) {
	bills := []BillPayment{{Due: day("2026-09-05"), Total: rat("1234.56")}}
	debits := []Candidate{
		{ID: "pay", Date: day("2026-09-04"), Amount: rat("1234.56"), Direction: "DEBIT"},
		{ID: "dup", Date: day("2026-09-04"), Amount: rat("1234.56"), Direction: "DEBIT"},
		{ID: "late", Date: day("2026-10-01"), Amount: rat("1234.56"), Direction: "DEBIT"},
		{ID: "other", Date: day("2026-09-04"), Amount: rat("99"), Direction: "DEBIT"},
	}
	got := MatchBillPayments(debits, bills)
	if len(got) != 1 || got[0] != "pay" {
		t.Errorf("got %v, want [pay]", got)
	}
}

func TestMapCategory(t *testing.T) {
	cases := []struct{ cat, desc, want string }{
		{"Groceries", "", CatFood},
		{"Eating out", "", CatFood},
		{"", "UBER *TRIP", CatTransport},
		{"Pharmacy", "", CatHealth},
		{"Airlines", "", CatTravel},
		{"Streaming", "", CatSubscriptions},
		// Category names seen in a real Pluggy account (Pluggy spells "Accomodation" with one m).
		{"Accomodation", "", CatTravel},
		{"Automotive", "", CatTransport},
		{"Houseware", "", CatHousing},
		{"Kids and toys", "", CatShopping},
		{"Office supplies", "", CatShopping},
		{"Pet supplies and vet", "", CatShopping},
		{"Stadiums and arenas", "", CatLeisure},
		{"Tickets", "", CatLeisure},
		{"Electricity", "", CatHousing},
		{"Wellness and fitness", "", CatHealth},
		{"Tolls and in vehicle payment", "", CatTransport},
		{"Interests charged", "", CatCardInterest},
		{"Transfer - PIX", "", ""},
		{"Whatever", "COISA ESTRANHA", ""},
	}
	for _, c := range cases {
		if got := MapExpenseCategory(c.cat, c.desc); got != c.want {
			t.Errorf("MapExpenseCategory(%q,%q) = %q, want %q", c.cat, c.desc, got, c.want)
		}
	}
	if MapIncomeCategory("Salary", "") != CatSalary || MapIncomeCategory("", "RENDIMENTO CDB") != CatYield || MapIncomeCategory("x", "y") != "" {
		t.Error("income mapping wrong")
	}
}

func TestBalanceTypeFor(t *testing.T) {
	if BalanceTypeFor("SAVINGS_ACCOUNT") != "SAVINGS" || BalanceTypeFor("CHECKING_ACCOUNT") != "CHECKING" || BalanceTypeFor("") != "CHECKING" {
		t.Error("balance type mapping wrong")
	}
}

func TestSyncUsable_AndAttention(t *testing.T) {
	if !SyncUsable("UPDATED", "SUCCESS") || !SyncUsable("UPDATED", "PARTIAL_SUCCESS") || SyncUsable("UPDATING", "") || SyncUsable("LOGIN_ERROR", "INVALID_CREDENTIALS") {
		t.Error("SyncUsable wrong")
	}
	if !NeedsAttention("LOGIN_ERROR", "") || NeedsAttention("UPDATED", "SUCCESS") {
		t.Error("NeedsAttention wrong")
	}
}
