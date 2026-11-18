package textmatch

import (
	"testing"
	"time"
)

func day(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

func TestTokens_DropNoiseAndCounters(t *testing.T) {
	got := Tokens("COMPRA CARTAO UBER *TRIP 03/06 PARC 2/5")
	if len(got) != 2 || got[0] != "uber" || got[1] != "trip" {
		t.Errorf("got %v, want [uber trip]", got)
	}
}

func TestShared(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"UBER *TRIP", "Uber Trip", 2},
		{"MERCADOLIVRE*ANEL", "Mercado Livre - anel", 2}, // prefix rule joins split brand names
		{"iFood - Bruno Dias Ramos", "IFOOD *BRUNO DIAS", 3},
		{"PADARIA DO ZE", "SUPERMERCADO BOM PRECO", 0},
		{"PIX ENVIADO", "PIX RECEBIDO", 0}, // pix/enviado-recebido carry no merchant
	}
	for _, c := range cases {
		if got := Shared(c.a, c.b); got < c.want || (c.want == 0 && got != 0) {
			t.Errorf("Shared(%q,%q) = %d, want >= %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPick(t *testing.T) {
	d := day("2026-08-10")
	t.Run("same day, description agrees", func(t *testing.T) {
		i, ok := Pick([]Candidate{{ID: "a", Date: d, Description: "Padaria"}, {ID: "b", Date: d, Description: "UBER TRIP"}}, d, "UBER *TRIP")
		if !ok || i != 1 {
			t.Errorf("got %d %v, want index 1", i, ok)
		}
	})
	t.Run("same day, only candidate, different naming", func(t *testing.T) {
		i, ok := Pick([]Candidate{{ID: "a", Date: d, Description: "Almoço"}}, d, "REST SABOR CASEIRO LTDA")
		if !ok || i != 0 {
			t.Errorf("sole same-day same-amount row must match, got %d %v", i, ok)
		}
	})
	t.Run("same day, two candidates, none agree: no guess", func(t *testing.T) {
		_, ok := Pick([]Candidate{{ID: "a", Date: d, Description: "Almoço"}, {ID: "b", Date: d, Description: "Cinema"}}, d, "REST SABOR CASEIRO")
		if ok {
			t.Error("must not guess between two unrelated same-day candidates")
		}
	})
	t.Run("day apart needs the merchant", func(t *testing.T) {
		if _, ok := Pick([]Candidate{{ID: "a", Date: day("2026-08-11"), Description: "Almoço"}}, d, "REST SABOR"); ok {
			t.Error("a different day with unrelated text must not match")
		}
		i, ok := Pick([]Candidate{{ID: "a", Date: day("2026-08-11"), Description: "UBER"}}, d, "UBER *TRIP")
		if !ok || i != 0 {
			t.Errorf("one day apart with the same merchant must match, got %d %v", i, ok)
		}
	})
	t.Run("too far apart never matches", func(t *testing.T) {
		if _, ok := Pick([]Candidate{{ID: "a", Date: day("2026-08-20"), Description: "UBER TRIP"}}, d, "UBER *TRIP"); ok {
			t.Error("ten days apart must not match even with the same merchant")
		}
	})
	t.Run("prefers exact date over near date", func(t *testing.T) {
		i, ok := Pick([]Candidate{{ID: "near", Date: day("2026-08-11"), Description: "UBER TRIP"}, {ID: "exact", Date: d, Description: "UBER TRIP"}}, d, "UBER *TRIP")
		if !ok || i != 1 {
			t.Errorf("got %d, want the exact-date candidate", i)
		}
	})
}

func TestPickSameStatement(t *testing.T) {
	// Same statement month: the stored row is dated at the competence, so date proximity is
	// irrelevant — the description decides, and amount+statement is the fallback.
	t.Run("description agrees", func(t *testing.T) {
		i, ok := PickSameStatement([]Candidate{
			{ID: "a", Description: "Almoço"},
			{ID: "b", Description: "CASAS BAHIA"},
		}, "Casas Bahia Com - Parcela 8 de 10")
		if !ok || i != 1 {
			t.Errorf("got %d %v, want index 1", i, ok)
		}
	})
	t.Run("sole candidate without shared words still links", func(t *testing.T) {
		i, ok := PickSameStatement([]Candidate{{ID: "a", Description: "PG CONTA"}}, "Associacao Educacional")
		if !ok || i != 0 {
			t.Errorf("sole same-statement same-amount row must match, got %d %v", i, ok)
		}
	})
	t.Run("two unrelated candidates: no guess", func(t *testing.T) {
		if _, ok := PickSameStatement([]Candidate{{ID: "a", Description: "Almoço"}, {ID: "b", Description: "Cinema"}}, "REST SABOR CASEIRO"); ok {
			t.Error("must not guess between two unrelated same-statement candidates")
		}
	})
	t.Run("no candidates", func(t *testing.T) {
		if _, ok := PickSameStatement(nil, "Qualquer"); ok {
			t.Error("empty candidate list must not match")
		}
	})
}
