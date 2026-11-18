// Package cardspend holds the one rule that decides which month a credit-card charge counts in.
//
// A card charge has three dates: when the card was used (purchase), which statement it is billed
// on (competência) and when that statement is paid (vencimento). Finances reports "gastos do mês"
// by when the money was committed, so a charge counts on its purchase date — except an installment,
// which counts in the month it is actually billed: installment n of a purchase made on 22/01 counts
// on 22 of the month n-1 months later (5/10 → 22/05). Paying the statement is cash, not spending,
// and is reported from credit_card_bills by due date instead.
package cardspend

import (
	"regexp"
	"strconv"
	"time"
)

// SpendDate is the date a card charge counts in: purchase shifted (n-1) months for installment n.
// The day is clamped to the target month (31/01 + 1 month → 28/02), matching Postgres
// `date + interval 'n months'`.
func SpendDate(purchase time.Time, installmentNumber int) time.Time {
	if installmentNumber <= 1 {
		return purchase
	}
	y, m, d := purchase.Date()
	target := time.Date(y, m+time.Month(installmentNumber-1), 1, 0, 0, 0, 0, purchase.Location())
	if last := time.Date(target.Year(), target.Month()+1, 0, 0, 0, 0, 0, purchase.Location()).Day(); d > last {
		d = last
	}
	return time.Date(target.Year(), target.Month(), d, 0, 0, 0, 0, purchase.Location())
}

// installmentSuffix matches the "n/N" a statement line ends with ("CASASBAHIACOM 05/10",
// "MERCADOLIVRE*ELEME03/10", "Loja X (3/12)").
var installmentSuffix = regexp.MustCompile(`(\d{1,2})/(\d{1,2})\)?\s*$`)

// ParseInstallment extracts "n/N" from the end of a statement description. ok is false when there
// is none or it is not a plausible installment (1 <= n <= N, N >= 2).
func ParseInstallment(description string) (n, total int, ok bool) {
	m := installmentSuffix.FindStringSubmatch(description)
	if m == nil {
		return 0, 0, false
	}
	n, _ = strconv.Atoi(m[1])
	total, _ = strconv.Atoi(m[2])
	if total < 2 || n < 1 || n > total {
		return 0, 0, false
	}
	return n, total, true
}

// SpendDateFromDescription is SpendDate with the installment read from the description.
func SpendDateFromDescription(purchase time.Time, description string) time.Time {
	if n, _, ok := ParseInstallment(description); ok {
		return SpendDate(purchase, n)
	}
	return purchase
}
