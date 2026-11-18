package categorize

import "testing"

func TestSuggest_ExactAndContainment(t *testing.T) {
	m := NewMemory([]Example{
		{Description: "Uber", Category: "Transporte", Manual: true},
		{Description: "Mercado Livre", Category: "Compras", Manual: true},
		{Description: "Netflix.com", Category: "Assinaturas", Manual: true},
	})

	cases := []struct {
		desc string
		want string
	}{
		{"Uber", "Transporte"},           // exact
		{"UBER *TRIP", "Transporte"},     // extra token, containment
		{"Mercadolivre 3/12", "Compras"}, // prefix + installment noise
		{"NETFLIX.COM", "Assinaturas"},   // exact after folding
	}
	for _, c := range cases {
		got, ok := m.Suggest(c.desc)
		if !ok || got.Category != c.want {
			t.Errorf("Suggest(%q) = %+v, %v; want %q, true", c.desc, got, ok, c.want)
		}
		if !got.Strong {
			t.Errorf("Suggest(%q) should be strong", c.desc)
		}
	}
}

func TestSuggest_SingleSharedWordDoesNotMatch(t *testing.T) {
	m := NewMemory([]Example{{Description: "Drogaria Sao Paulo", Category: "Saúde", Manual: true}})
	if got, ok := m.Suggest("Drogaria Central"); ok {
		t.Fatalf("a single shared generic word must not link two different merchants: %+v", got)
	}
}

func TestSuggest_WeakMatchOnTwoSharedWords(t *testing.T) {
	m := NewMemory([]Example{{Description: "Mercado Livre Centro", Category: "Compras", Manual: true}})
	got, ok := m.Suggest("Mercado Livre Express")
	if !ok || got.Category != "Compras" {
		t.Fatalf("Suggest = %+v, %v; want Compras (two shared words)", got, ok)
	}
	if got.Strong {
		t.Fatalf("two shared words is a weak match: %+v", got)
	}
}

func TestSuggest_NoMatchOnUnrelated(t *testing.T) {
	m := NewMemory([]Example{{Description: "Padaria Central", Category: "Alimentação", Manual: true}})
	if got, ok := m.Suggest("Oficina Mecanica Silva"); ok {
		t.Fatalf("unrelated description matched: %+v", got)
	}
}

func TestSuggest_ManualWinsOverFrequency(t *testing.T) {
	m := NewMemory([]Example{
		{Description: "Amazon", Category: "Compras", Manual: false},
		{Description: "Amazon", Category: "Compras", Manual: false},
		{Description: "Amazon", Category: "Assinaturas", Manual: true},
	})
	got, ok := m.Suggest("Amazon")
	if !ok || got.Category != "Assinaturas" {
		t.Fatalf("Suggest = %+v, %v; want Assinaturas (manual wins)", got, ok)
	}
}

func TestSuggest_MostFrequentAmongAuto(t *testing.T) {
	m := NewMemory([]Example{
		{Description: "Posto Ipiranga", Category: "Transporte"},
		{Description: "Posto Ipiranga", Category: "Transporte"},
		{Description: "Posto Ipiranga", Category: "Outros"},
	})
	got, ok := m.Suggest("Posto Ipiranga")
	if !ok || got.Category != "Transporte" {
		t.Fatalf("Suggest = %+v, %v; want Transporte (most frequent)", got, ok)
	}
}

func TestSuggest_EmptyAndNoise(t *testing.T) {
	m := NewMemory([]Example{{Description: "Uber", Category: "Transporte"}})
	if _, ok := m.Suggest(""); ok {
		t.Fatal("empty description must not match")
	}
	if _, ok := m.Suggest("PIX PAGAMENTO"); ok {
		t.Fatal("all-noise description must not match")
	}
}

func TestNewMemory_DropsUnusableExamples(t *testing.T) {
	m := NewMemory([]Example{
		{Description: "", Category: "Compras"},
		{Description: "Uber", Category: ""},
		{Description: "Pix", Category: "Outros"},
		{Description: "Uber", Category: "Transporte"},
	})
	if m.Len() != 1 {
		t.Fatalf("Len = %d, want 1", m.Len())
	}
}
