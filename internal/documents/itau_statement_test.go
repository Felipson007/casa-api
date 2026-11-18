package documents

import (
	"bytes"
	"errors"
	"testing"

	"github.com/go-pdf/fpdf"
)

// itauLine is one cell row of a synthetic statement: x is the column's left edge (pt), y the
// baseline from the top of the page.
type itauLine struct {
	x, y  float64
	cells []string
}

// fakeItauStatement renders a two-column statement shaped like Itaú's: the two columns' rows are
// deliberately NOT aligned, so a plain-text read interleaves them.
func fakeItauStatement(t *testing.T, lines []itauLine) []byte {
	t.Helper()
	doc := fpdf.New("P", "pt", "A4", "")
	tr := doc.UnicodeTranslatorFromDescriptor("")
	doc.SetFont("Arial", "", 8)
	doc.AddPage()
	for _, l := range lines {
		x := l.x
		for _, c := range l.cells {
			doc.Text(x, l.y, tr(c))
			x += doc.GetStringWidth(tr(c)) + 20
		}
	}
	var buf bytes.Buffer
	if err := doc.Output(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func header() []itauLine {
	return []itauLine{
		{140, 40, []string{"Banco Itaú S.A."}},
		{140, 52, []string{"Emissão: 25/09/2026"}},
		{140, 64, []string{"Vencimento: 08/10/2026"}},
		{360, 52, []string{"Lançamentos atuais", "195,70"}},
		{140, 100, []string{"Pagamentos efetuados"}},
		{140, 110, []string{"27/08", "Pagamento via conta", "-999,00"}},
	}
}

func TestParseItauStatementTwoColumns(t *testing.T) {
	lines := append(header(),
		itauLine{140, 130, []string{"Lançamentos: compras e saques"}},
		itauLine{140, 140, []string{"22/01", "CASASBAHIACOM 09/10", "100,00"}},
		itauLine{140, 149, []string{"vestuário  RIO DE JANEIR"}},
		itauLine{140, 160, []string{"28/12", "LOJA ANTIGA", "10,00"}},
		itauLine{140, 175, []string{"Lançamentos internacionais"}},
		itauLine{140, 185, []string{"04/09www.loja.com", "17,77"}},
		itauLine{140, 194, []string{"3,23USD", "3,23"}},
		itauLine{140, 205, []string{"Repasse de IOF em R$", "0,62"}},
		itauLine{140, 215, []string{"Total lançamentos  inter. em R$", "18,39"}},
		// Right column, rows offset from the left column's.
		itauLine{360, 133, []string{"Lançamentos: compras e saques"}},
		itauLine{360, 145, []string{"28/08", "Uber UBER *TRIP HELP.US", "20,92"}},
		itauLine{360, 154, []string{"transporte  SAO PAULO"}},
		itauLine{360, 165, []string{"28/08", "Uber UBER *TRIP HELP.US", "20,92"}},
		itauLine{360, 180, []string{"13/09", "ESTORNO LOJA", "-", "5,00"}},
		itauLine{360, 195, []string{"Lançamentos: produtos e serviços"}},
		itauLine{360, 205, []string{"13/09", "ANUIDADE DIFERENCI01/12", "40,00"}},
		itauLine{360, 220, []string{"Total dos lançamentos atuais", "195,70"}},
		itauLine{360, 240, []string{"Compras parceladas - próximas faturas"}},
		itauLine{360, 250, []string{"22/01", "CASASBAHIACOM 10/10", "100,00"}},
	)
	// 100 + 10 + 17.77 + 0.62 + 20.92 + 20.92 - 5 + 40 = 205.23 — make the printed total match.
	for i := range lines {
		for j, c := range lines[i].cells {
			if c == "195,70" {
				lines[i].cells[j] = "205,23"
			}
		}
	}
	st, err := parseItauStatement(fakeItauStatement(t, lines))
	if err != nil {
		t.Fatalf("parseItauStatement: %v", err)
	}
	if got := st.closingDate.Format("2006-01-02"); got != "2026-09-25" {
		t.Errorf("closing = %s", got)
	}
	if got := st.dueDate.Format("2006-01-02"); got != "2026-10-08" {
		t.Errorf("due = %s", got)
	}
	want := []struct{ date, desc, amount, category string }{
		{"2026-01-22", "CASASBAHIACOM 09/10", "100.00", "Compras"},
		{"2025-12-28", "LOJA ANTIGA", "10.00", ""},
		{"2026-09-04", "www.loja.com", "17.77", ""},
		{"2026-09-04", "Repasse de IOF (compras internacionais)", "0.62", "Tarifas e IOF"},
		{"2026-08-28", "Uber UBER *TRIP HELP.US", "20.92", "Transporte"},
		{"2026-08-28", "Uber UBER *TRIP HELP.US", "20.92", ""},
		{"2026-09-13", "ESTORNO LOJA", "-5.00", "Ajustes e Estornos"},
		{"2026-09-13", "ANUIDADE DIFERENCI01/12", "40.00", "Tarifas e IOF"},
	}
	if len(st.items) != len(want) {
		for _, it := range st.items {
			t.Logf("%s %q %s", it.date.Format("2006-01-02"), it.description, it.amount)
		}
		t.Fatalf("got %d lines, want %d", len(st.items), len(want))
	}
	for i, w := range want {
		it := st.items[i]
		if it.date.Format("2006-01-02") != w.date || it.description != w.desc || it.amount != w.amount || it.category != w.category {
			t.Errorf("line %d = %s %q %s %q, want %s %q %s %q", i, it.date.Format("2006-01-02"), it.description, it.amount, it.category, w.date, w.desc, w.amount, w.category)
		}
	}
}

func TestParseItauStatementRejectsUnbalanced(t *testing.T) {
	lines := append(header(),
		itauLine{140, 130, []string{"Lançamentos: compras e saques"}},
		itauLine{140, 140, []string{"10/09", "LOJA", "100,00"}},
	)
	if _, err := parseItauStatement(fakeItauStatement(t, lines)); !errors.Is(err, errItauUnbalance) {
		t.Fatalf("err = %v, want errItauUnbalance", err)
	}
}

func TestParseItauStatementIgnoresOtherDocuments(t *testing.T) {
	lines := []itauLine{{140, 40, []string{"Nubank"}}, {140, 60, []string{"10/09", "LOJA", "100,00"}}}
	if _, err := parseItauStatement(fakeItauStatement(t, lines)); !errors.Is(err, errNotItau) {
		t.Fatalf("err = %v, want errNotItau", err)
	}
}
