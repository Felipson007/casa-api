// Package textmatch is the one place that decides "is this the same purchase?" when the same
// spending reaches Atlas through two doors — a bank sync (Open Finance) and a hand-typed row or an
// uploaded invoice. Both directions of that de-duplication (internal/openfinance linking an
// existing row, internal/expenses adopting a synced row when an invoice is uploaded) use Pick, so
// the rule is written, tested and tuned once.
//
// The rule is deliberately conservative about what counts as "the same", and deliberately safe
// about being wrong: a wrong match LINKS two rows (nothing is lost, at worst one real expense is
// hidden behind a look-alike), while a missed match would DOUBLE-COUNT money. Amount equality is
// checked by the caller in SQL; this package judges date and description.
package textmatch

import (
	"strings"
	"time"
)

var fold = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "ê", "e", "è", "e", "ë", "e",
	"í", "i", "î", "i", "ì", "i", "ï", "i",
	"ó", "o", "ô", "o", "õ", "o", "ò", "o", "ö", "o",
	"ú", "u", "û", "u", "ù", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

// noise are tokens that say nothing about WHO was paid: statement boilerplate, payment-method
// words and Portuguese glue. Matching on them would make every pair of purchases "similar".
var noise = map[string]bool{
	"de": true, "do": true, "da": true, "dos": true, "das": true, "e": true, "em": true, "no": true, "na": true,
	"compra": true, "compras": true, "cartao": true, "credito": true, "debito": true, "pagamento": true,
	"pgto": true, "pagto": true, "pag": true, "parcela": true, "parc": true, "pix": true, "ted": true, "doc": true,
	"br": true, "brasil": true, "ltda": true, "sa": true, "me": true, "eireli": true, "cia": true,
	"com": true, "www": true, "the": true, "and": true,
}

// Tokens returns the significant lower-case, accent-folded words of s (length ≥ 3, not noise, not
// purely numeric — card statements append installment counters and terminal ids that differ
// between sources for the very same purchase).
func Tokens(s string) []string {
	s = fold.Replace(strings.ToLower(s))
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') }) {
		if len(w) < 3 || noise[w] || allDigits(w) || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

func allDigits(w string) bool {
	for _, r := range w {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// TokenEqual reports whether two significant tokens name the same word: exact, or one a prefix of
// the other (≥4 chars) — "MERCADOLIVRE"/"MERCADO LIVRE", "UBER"/"UBERTRIP". Exported so the
// category memory (internal/categorize) reuses the very same word-identity rule the de-duplication
// is tuned on, instead of inventing a second, divergent one.
func TokenEqual(a, b string) bool {
	return a == b || (len(a) >= 4 && len(b) >= 4 && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a)))
}

// Shared counts the significant words a and b have in common. A token also counts when one is a
// prefix of the other (≥4 chars): "MERCADOLIVRE" vs "MERCADO LIVRE", "UBER" vs "UBERTRIP".
func Shared(a, b string) int {
	ta, tb := Tokens(a), Tokens(b)
	n := 0
	for _, x := range ta {
		for _, y := range tb {
			if TokenEqual(x, y) {
				n++
				break
			}
		}
	}
	return n
}

// Candidate is an already-stored row with the same amount as the incoming one.
type Candidate struct {
	ID          string
	Date        time.Time // the row's purchase date
	Description string
}

// dayWindow is how far apart two dates may be and still be the same purchase — only when the
// descriptions vouch for it. Banks and statements disagree on purchase vs posting date by a day or
// two, but never on the merchant.
const dayWindow = 2

// Pick chooses which stored candidate (if any) is the same purchase as the incoming one, or
// reports ok=false when none is. Rules, in order:
//
//  1. same date and the description shares a significant word → match (best overlap wins);
//  2. same date, and it is the ONLY candidate with that amount and date → match (same amount, same
//     day and nothing else competing is the same purchase, even when the two sources name the
//     merchant differently);
//  3. within ±2 days and the description shares a significant word → match.
//
// One candidate is consumed per incoming row (the caller removes/links it), so two identical
// purchases on the same day match two rows one-to-one and a surplus incoming row stays new.
func Pick(cands []Candidate, date time.Time, description string) (int, bool) {
	best, bestScore := -1, -1
	sameDay := 0
	for _, c := range cands {
		if sameDate(c.Date, date) {
			sameDay++
		}
	}
	for i, c := range cands {
		shared := Shared(c.Description, description)
		gap := dayGap(c.Date, date)
		var score int
		switch {
		case gap == 0 && shared > 0:
			score = 1000 + shared*10
		case gap == 0 && sameDay == 1:
			score = 500
		case gap > 0 && gap <= dayWindow && shared > 0:
			score = 100 + shared*10 - gap
		default:
			continue
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best, best >= 0
}

// PickSameStatement chooses among candidates already known to belong to the SAME card statement as
// the incoming row — the case where the stored date is the statement/posting date (or a line from an
// imported invoice dated at the competence) rather than the purchase date, so the ±2-day rule from
// Pick doesn't apply. Same amount (and same statement month) is guaranteed by the caller.
//
// A shared significant word wins. With no shared word and a single candidate, the amount+statement
// pair is still strong enough to link — matching here is deliberate: a wrong link hides one expense
// behind a look-alike, while a missed link double-counts the money (see the package comment).
func PickSameStatement(cands []Candidate, description string) (int, bool) {
	if len(cands) == 0 {
		return -1, false
	}
	best, bestScore := -1, -1
	for i, c := range cands {
		if s := Shared(c.Description, description); s > bestScore {
			best, bestScore = i, s
		}
	}
	if bestScore > 0 {
		return best, true
	}
	if len(cands) == 1 {
		return 0, true
	}
	return -1, false
}

func sameDate(a, b time.Time) bool { return dayGap(a, b) == 0 }

func dayGap(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	ua := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	ub := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	d := int(ua.Sub(ub).Hours() / 24)
	if d < 0 {
		d = -d
	}
	return d
}
