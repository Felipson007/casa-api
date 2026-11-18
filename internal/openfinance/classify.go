package openfinance

import (
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/atlas/atlas-go/internal/pluggy"
)

// ---- text normalisation --------------------------------------------------------------------

var accentFold = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "ê", "e", "è", "e", "ë", "e",
	"í", "i", "î", "i", "ì", "i", "ï", "i",
	"ó", "o", "ô", "o", "õ", "o", "ò", "o", "ö", "o",
	"ú", "u", "û", "u", "ù", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

// normalize lower-cases, folds Portuguese accents and collapses punctuation to spaces, so the
// keyword rules below match "PAGAMENTO DE FATURA", "Pagamento de fatura" and "pgto. fatura" alike.
func normalize(s string) string {
	s = accentFold.Replace(strings.ToLower(s))
	var b strings.Builder
	prevSpace := true
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	return " " + strings.TrimSpace(b.String()) + " "
}

func hasAny(text string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(text, n) {
			return true
		}
	}
	return false
}

// ---- classification ------------------------------------------------------------------------

// ClassifyInput is what the classifier needs from one raw transaction.
type ClassifyInput struct {
	AccountType      string // BANK | CREDIT
	Direction        string // DEBIT | CREDIT
	Amount           pluggy.Decimal
	Status           string // PENDING | POSTED
	Description      string
	ProviderCategory string
}

var (
	cardWords    = []string{" cartao ", " cartoes ", " card ", " itaucard ", " nubank ", " visa ", " mastercard ", " master ", " elo ", " amex ", " hipercard ", " credicard "}
	utilityWords = []string{" energia ", " luz ", " agua ", " gas ", " telefone ", " internet ", " vivo ", " claro ", " tim ", " oi ", " net ", " enel ", " cpfl ", " sabesp ", " copel ", " condominio "}
)

// isCardPaymentText: does this description/category say "paying a card statement"?
func isCardPaymentText(desc, category string) bool {
	if hasAny(category, "credit card payment", "pagamento de cartao", "pagamento cartao", "pagamento de fatura") {
		return true
	}
	if !hasAny(desc, " fatura ", " faturas ") {
		return hasAny(desc, "pagamento cartao", "pgto cartao", "pagto cartao", "pag cartao")
	}
	if hasAny(desc, cardWords...) {
		return true
	}
	// "pagamento de fatura" with no card word is ambiguous — utilities also send "faturas".
	if hasAny(desc, utilityWords...) {
		return false
	}
	return hasAny(desc, "pagamento de fatura", "pagamento fatura", "pag fatura", "pgto fatura", "pagto fatura")
}

// isStatementPaymentText recognises a payment credited to a card account.
func isStatementPaymentText(desc, category string) bool {
	return isCardPaymentText(desc, category) || hasAny(desc, "pagamento", " pgto ", " pagto ", "payment", "debito em conta")
}

func isInvestmentText(desc, category string) bool {
	if hasAny(category, "invest", "fixed income", "renda fixa", "variable income", "renda variavel", "mutual fund", "fundos", "retirement", "previdencia") {
		return true
	}
	return hasAny(desc, "aplicacao", " aplic ", " aplic aut", "resgate", " resg ", " cdb ", " rdb ", " lci ", " lca ", "tesouro", "invest facil", "investimento")
}

func isOwnTransferText(desc, category string) bool {
	if hasAny(category, "same person", "own account", "mesma titularidade", "entre contas") {
		return true
	}
	return hasAny(desc, "transferencia entre contas", "transf entre contas", "mesma titularidade", "transf propria", "trf propria", "transferencia propria")
}

// IsTransferLike is the pre-filter for pairing debits with credits across the household's own
// accounts: only movements that look like a transfer are candidates, so an unrelated purchase and
// an unrelated deposit of the same amount are never merged.
func IsTransferLike(desc, category string) bool {
	d, c := normalize(desc), normalize(category)
	return hasAny(d, " pix ", " ted ", " doc ", "transf", "transfer") || hasAny(c, "transfer", "pix", " ted ")
}

// Classify decides what one raw transaction becomes. It never returns KindIgnoredManual (that only
// comes from a human override). Order matters: the specific "not real spending" rules run before
// the generic debit/credit split.
func Classify(in ClassifyInput) ProjectionKind {
	// A pending BANK movement may still bounce, so it waits. A pending CARD charge is an authorised
	// purchase on the open statement — Itaú keeps the whole open statement PENDING until it closes,
	// so waiting would hide a month of spending. If the authorisation is reversed the row disappears
	// from the feed and its projection is retracted.
	if in.Status == "PENDING" && in.AccountType != AccountTypeCredit {
		return KindPending
	}
	desc, cat := normalize(in.Description), normalize(in.ProviderCategory)

	if in.AccountType == AccountTypeCredit {
		// Money paying the statement down. Connectors disagree on its sign ("Pagamento recebido"
		// arrives positive from some), so the movement type decides before the sign does. A charge
		// mentioning "pagamento" (e.g. "JUROS PAGAMENTO CONTAS") is a DEBIT and is not caught here.
		if in.Direction == "CREDIT" && isStatementPaymentText(desc, cat) {
			return KindIgnoredCardPayment
		}
		if in.Amount.Sign() > 0 {
			return KindExpense // a charge: this is where card spending is counted
		}
		if in.Amount.Sign() < 0 {
			if isStatementPaymentText(desc, cat) {
				return KindIgnoredCardPayment // money paying the statement down, not spending
			}
			return KindExpense // a refund/estorno: recorded as a negative expense
		}
		return KindPending
	}

	// BANK account.
	switch {
	case isCardPaymentText(desc, cat):
		return KindIgnoredCardPayment
	case isOwnTransferText(desc, cat):
		return KindIgnoredTransfer
	case isInvestmentText(desc, cat):
		return KindIgnoredInvestment
	}
	if in.Direction == "CREDIT" {
		return KindIncome
	}
	return KindExpense
}

// ---- statements ----------------------------------------------------------------------------

// BillCompetence returns the reference month (first day, YYYY-MM-01) of a card statement, the same
// "competência" convention the PDF invoice flow uses (closing month).
//
// Pluggy's Bill carries a due date but, as far as its documentation shows, no closing date, so the
// month is inferred: the latest purchase attached to the statement (the cycle's last day is the
// closing day), or — with no purchases attached — ten days before the due date, the usual gap
// between closing and due. Confirm against real payloads; this is the one heuristic in the
// module and it is isolated here on purpose.
func BillCompetence(b pluggy.Bill, purchaseDates []string) string {
	latest := ""
	for _, d := range purchaseDates {
		if len(d) >= 10 && d[:10] > latest {
			latest = d[:10]
		}
	}
	if latest != "" {
		return latest[:7] + "-01"
	}
	if due, err := time.Parse("2006-01-02", pluggy.DateOf(b.DueDate)); err == nil {
		return due.AddDate(0, 0, -10).Format("2006-01") + "-01"
	}
	return ""
}

// BillFinanceCharges sums a statement's interest/IOF/fees.
func BillFinanceCharges(b pluggy.Bill) string {
	sum := new(big.Rat)
	for _, c := range b.FinanceCharges {
		sum.Add(sum, c.Amount.Rat())
	}
	return sum.FloatString(2)
}

// ---- cross-account passes ------------------------------------------------------------------

// Candidate is a bank-account transaction considered by the cross-account passes.
type Candidate struct {
	ID        string
	AccountID string
	Date      time.Time
	Amount    *big.Rat // absolute value
	Direction string   // DEBIT | CREDIT
}

// PairTransfers finds debit/credit pairs that are the same money moving between two different
// accounts: equal amount, opposite direction, within one day, one-to-one. Returns every id that
// belongs to a pair. The candidates must already be pre-filtered with IsTransferLike.
func PairTransfers(cands []Candidate) []string {
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Date.Before(cands[j].Date) })
	used := make([]bool, len(cands))
	var out []string
	for i, d := range cands {
		if used[i] || d.Direction != "DEBIT" {
			continue
		}
		best, bestGap := -1, 2*24*time.Hour
		for j, c := range cands {
			if used[j] || c.Direction != "CREDIT" || c.AccountID == d.AccountID || c.Amount.Cmp(d.Amount) != 0 {
				continue
			}
			gap := c.Date.Sub(d.Date)
			if gap < 0 {
				gap = -gap
			}
			if gap <= 24*time.Hour && gap < bestGap {
				best, bestGap = j, gap
			}
		}
		if best >= 0 {
			used[i], used[best] = true, true
			out = append(out, d.ID, cands[best].ID)
		}
	}
	return out
}

// BillPayment is a statement's expected settlement.
type BillPayment struct {
	Due   time.Time
	Total *big.Rat
}

// MatchBillPayments returns the ids of bank debits that settle a card statement: same amount as a
// statement total, dated between ten days before its due date and five days after (early payment,
// weekend/holiday slip). One debit per statement.
func MatchBillPayments(debits []Candidate, bills []BillPayment) []string {
	usedBill := make([]bool, len(bills))
	var out []string
	for _, d := range debits {
		if d.Direction != "DEBIT" {
			continue
		}
		for i, b := range bills {
			if usedBill[i] || b.Total.Sign() <= 0 || b.Total.Cmp(d.Amount) != 0 {
				continue
			}
			if d.Date.Before(b.Due.AddDate(0, 0, -10)) || d.Date.After(b.Due.AddDate(0, 0, 5)) {
				continue
			}
			usedBill[i] = true
			out = append(out, d.ID)
			break
		}
	}
	return out
}
