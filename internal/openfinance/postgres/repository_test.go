package postgres

import (
	"testing"

	"github.com/atlas/atlas-go/internal/categorize"
)

func TestAutoCategory(t *testing.T) {
	memory := categorize.NewMemory([]categorize.Example{
		{Description: "Uber", Category: "Transporte", Manual: true},
	})
	rulesOnly := categorize.NewMemory(nil)

	tests := []struct {
		name         string
		memory       categorize.Memory
		ruleCategory string
		description  string
		wantCategory string
		wantSource   string
	}{
		{"strong history beats the rule", memory, "Outros", "UBER *TRIP", "Transporte", "HISTORY"},
		{"rule when nothing matches", rulesOnly, "Compras", "Padaria Central", "Compras", "RULE"},
		{"no rule and no match is NONE", rulesOnly, "", "Padaria Central", "", "NONE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			category, source := autoCategory(tt.memory, tt.ruleCategory, tt.description)
			if category != tt.wantCategory || source != tt.wantSource {
				t.Fatalf("autoCategory = (%q, %q), want (%q, %q)", category, source, tt.wantCategory, tt.wantSource)
			}
		})
	}
}

func TestAutoCategory_WeakMatchIsLeftForReview(t *testing.T) {
	memory := categorize.NewMemory([]categorize.Example{
		{Description: "Mercado Livre Centro", Category: "Compras"},
	})
	category, source := autoCategory(memory, "", "Mercado Livre Express")
	if category != "" || source != "NONE" {
		t.Fatalf("a weak match must stay uncategorised for manual review: got (%q, %q)", category, source)
	}
}
