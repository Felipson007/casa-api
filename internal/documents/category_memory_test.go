package documents

import (
	"strings"
	"testing"

	"github.com/atlas/atlas-go/internal/categorize"
)

func TestApplyCategoryMemory(t *testing.T) {
	memory := categorize.NewMemory([]categorize.Example{
		{Description: "Uber", Category: "Transporte", Manual: true},
	})
	items := []parsedInvoiceItem{
		{description: "UBER *TRIP", category: "Outros"},
		{description: "Padaria Nova", category: ""},
		{description: "Netflix.com", category: "Assinaturas"},
	}

	applyCategoryMemory(items, memory)

	if items[0].category != "Transporte" || items[0].categorySource != "HISTORY" {
		t.Fatalf("strong history match should win over the model: %+v", items[0])
	}
	if items[1].category != "" || items[1].categorySource != "NONE" {
		t.Fatalf("an unmatched blank line stays NONE: %+v", items[1])
	}
	if items[2].category != "Assinaturas" || items[2].categorySource != "AI" {
		t.Fatalf("an unmatched model category is kept as AI: %+v", items[2])
	}
}

func TestApplyCategoryMemory_FillsBlankFromHistory(t *testing.T) {
	memory := categorize.NewMemory([]categorize.Example{
		{Description: "Mercado Livre", Category: "Compras", Manual: true},
	})
	items := []parsedInvoiceItem{{description: "Mercado Livre 3/12", category: ""}}
	applyCategoryMemory(items, memory)
	if items[0].category != "Compras" || items[0].categorySource != "HISTORY" {
		t.Fatalf("a blank line with a history match should be filled: %+v", items[0])
	}
}

func TestInvoiceFormatInstructionFor(t *testing.T) {
	base := invoiceFormatInstructionFor(nil)
	if base != invoiceFormatInstruction {
		t.Fatal("no examples must return the base instruction unchanged")
	}

	withHistory := invoiceFormatInstructionFor([]categorize.Example{
		{Description: "Uber", Category: "Transporte"},
		{Description: "Uber", Category: "Transporte"}, // duplicate collapsed
		{Description: "", Category: "Outros"},         // unusable dropped
	})
	if !strings.Contains(withHistory, "Uber → Transporte") {
		t.Fatalf("expected the known mapping in the instruction, got:\n%s", withHistory)
	}
	if strings.Count(withHistory, "Uber → Transporte") != 1 {
		t.Fatalf("duplicate mappings must be collapsed, got:\n%s", withHistory)
	}
}
