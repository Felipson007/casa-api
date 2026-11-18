// Package categorize is the household's category memory: it learns "this merchant is that
// category" from launches already classified and reuses it for new launches whose description is
// the same or similar. It is what makes an imported invoice line, a bank-synced charge and a
// hand-typed row agree on one category across months, instead of each ingestion path guessing on
// its own.
//
// It is deliberately pure — no I/O, no database — so both the Open Finance projection and the
// invoice-extraction flow build the same memory from their own history and always apply the same
// rule. Description comparison reuses internal/platform/textmatch's tokenisation, so the notion of
// "same merchant" is the one already tuned for de-duplication.
package categorize

import (
	"strings"

	"github.com/atlas/atlas-go/internal/platform/textmatch"
)

// Example is one already-classified launch the memory can learn from: the merchant description and
// the category it ended up in. Manual marks a category a human chose (or corrected) — it is
// authoritative and takes precedence over any machine-assigned one in a tie.
type Example struct {
	Description string
	Category    string
	Manual      bool
}

// Suggestion is the category the memory proposes for a new description. Strong is true when the
// description matched an example's tokens exactly or by containment (the shorter description's
// significant words all appear in the longer) — safe to prefer over a keyword rule or a model
// guess. A weak suggestion only shares one significant word, so callers use it as a fallback.
type Suggestion struct {
	Category string
	Strong   bool
}

type example struct {
	category string
	manual   bool
	tokens   []string
}

// Memory is an immutable lookup built once from a batch of examples and queried for each incoming
// launch. The zero value is empty and valid.
type Memory struct {
	examples []example
}

// NewMemory builds the memory from the household's classified launches. Examples without a
// category or without any significant token in the description are dropped — they would match
// everything (or nothing) and only add noise.
func NewMemory(examples []Example) Memory {
	var m Memory
	for _, e := range examples {
		category := strings.TrimSpace(e.Category)
		if category == "" {
			continue
		}
		tokens := textmatch.Tokens(e.Description)
		if len(tokens) == 0 {
			continue
		}
		m.examples = append(m.examples, example{category: category, manual: e.Manual, tokens: tokens})
	}
	return m
}

// Len reports how many usable examples the memory holds.
func (m Memory) Len() int { return len(m.examples) }

// Suggest returns the category the memory proposes for description. ok is false when no example is
// similar enough (or the description has no significant words). Among equally good matches a manual
// example wins, then the most frequent category, then the alphabetically first — deterministic on
// purpose so the same history always yields the same answer.
func (m Memory) Suggest(description string) (Suggestion, bool) {
	incoming := textmatch.Tokens(description)
	if len(incoming) == 0 {
		return Suggestion{}, false
	}

	bestTier := 0
	for _, ex := range m.examples {
		if tier := matchTier(incoming, ex.tokens); tier > bestTier {
			bestTier = tier
		}
	}
	if bestTier == 0 {
		return Suggestion{}, false
	}

	counts := map[string]int{}
	manual := map[string]bool{}
	for _, ex := range m.examples {
		if matchTier(incoming, ex.tokens) != bestTier {
			continue
		}
		counts[ex.category]++
		if ex.manual {
			manual[ex.category] = true
		}
	}

	manualWinner, manualCount := "", 0
	autoWinner, autoCount := "", 0
	for category, count := range counts {
		if manual[category] {
			if count > manualCount || (count == manualCount && (manualWinner == "" || category < manualWinner)) {
				manualWinner, manualCount = category, count
			}
			continue
		}
		if count > autoCount || (count == autoCount && (autoWinner == "" || category < autoWinner)) {
			autoWinner, autoCount = category, count
		}
	}

	winner := manualWinner
	if winner == "" {
		winner = autoWinner
	}
	if winner == "" {
		return Suggestion{}, false
	}
	return Suggestion{Category: winner, Strong: bestTier >= tierContainment}, true
}

const (
	tierWeak        = 1
	tierContainment = 2
	tierExact       = 3
)

// matchTier scores how well two token sets denote the same merchant:
//
//	3  exact match — the same significant words, no more, no less;
//	2  containment — every significant word of the shorter description appears in the longer, so
//	   "Uber" matches "Uber Trip" and "Mercado Livre" matches "Mercado Livre 3/12";
//	1  weak — two or more significant words in common ("Drogaria Sao Paulo" vs "Drogaria Central"
//	   share only one, so they stay apart);
//	0  no usable overlap.
func matchTier(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for _, x := range a {
		for _, y := range b {
			if textmatch.TokenEqual(x, y) {
				shared++
				break
			}
		}
	}
	switch {
	case shared == 0:
		return 0
	case shared == len(a) && shared == len(b):
		return tierExact
	case shared == len(a) || shared == len(b):
		return tierContainment
	case shared >= 2:
		return tierWeak
	default:
		return 0
	}
}
