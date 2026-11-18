package postgres

import "testing"

func TestIsPayrollCredit(t *testing.T) {
	cases := []struct {
		description string
		want        bool
	}{
		{"Salário ITAU UNIBANCO S.A.", true},
		{"Férias ITAU UNIBANCO S.A.", true},
		{"Entrada FOLHA DE PARTICIPACAO", true},
		{"Entrada FOLHA PAGTO 13. SALARIO", true},
		{"Salário Base", true},
		{"Adiantamento Dias De Férias", true},
		{"Pix recebido Eufraldisia Gonçalves Sanches", false},
		{"Pix devolvido Josivan da Silva Lima", false},
		{"Saída PAGAMENTOCONTA TIMCEL-SA", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isPayrollCredit(c.description); got != c.want {
			t.Errorf("isPayrollCredit(%q) = %v, want %v", c.description, got, c.want)
		}
	}
}
