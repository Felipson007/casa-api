package documents

import (
	"testing"
	"time"
)

func mustParseDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad test date %q: %v", s, err)
	}
	return d
}

func TestParseExameMarkers(t *testing.T) {
	raw := []ExameExtraidoMarcador{
		{Nome: "Colesterol Total", Valor: "185,00", Unidade: " mg/dL ", ReferenciaMin: "0", ReferenciaMax: "190,00"},
		{Nome: "  ", Valor: "92.00"},                   // dropped: no name
		{Nome: "Marcador Ilegível", Valor: "não lido"}, // dropped: unparseable value
		{Nome: "Glicemia de Jejum", Valor: "92.00"},
	}
	got := parseExameMarkers(raw)
	if len(got) != 2 {
		t.Fatalf("parseExameMarkers() returned %d marker(s), want 2 (blank name and unparseable value dropped): %+v", len(got), got)
	}
	if got[0].Nome != "Colesterol Total" || got[0].Valor != "185.00" || got[0].Unidade != "mg/dL" || got[0].ReferenciaMax != "190.00" {
		t.Errorf("first marker normalized wrong: %+v", got[0])
	}
	if got[1].Nome != "Glicemia de Jejum" || got[1].Valor != "92.00" {
		t.Errorf("second marker normalized wrong: %+v", got[1])
	}
}

func TestParseSignedAmount(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"1.234,56", "1234.56", true},
		{"R$ 1.234,56", "1234.56", true},
		{"-106,60", "-106.60", true},
		{"(106,60)", "-106.60", true},
		{"106,60-", "-106.60", true},
		{"12,98 CR", "-12.98", true},
		{"0,00", "0.00", true},
		{"", "", false},
		{"abc", "", false},
	}
	for _, c := range cases {
		got, ok := parseSignedAmount(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseSignedAmount(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestResolveClosingMonth_PriorityOrder(t *testing.T) {
	docDate := mustParseDay(t, "2026-08-10")
	itemDates := []time.Time{
		mustParseDay(t, "2026-07-03"), mustParseDay(t, "2026-07-20"), mustParseDay(t, "2026-07-28"),
	}

	// 1. the due date (vencimento) wins over everything — it IS the competência convention.
	got := resolveClosingMonth(Document{DocumentDate: &docDate}, "2026-08-25", "2026-09-08", itemDates)
	if want := mustParseDay(t, "2026-09-01"); !got.Equal(want) {
		t.Errorf("dueDate priority: got %v want %v", got, want)
	}

	// 2. no dueDate → DocumentDate month (historically holds the vencimento).
	got = resolveClosingMonth(Document{DocumentDate: &docDate}, "2026-07-25", "", itemDates)
	if want := mustParseDay(t, "2026-08-01"); !got.Equal(want) {
		t.Errorf("documentDate priority: got %v want %v", got, want)
	}

	// 3. only a closing date → the month AFTER it (a statement closes the month before it's due).
	got = resolveClosingMonth(Document{}, "2026-08-25", "", itemDates)
	if want := mustParseDay(t, "2026-09-01"); !got.Equal(want) {
		t.Errorf("closingDate+1 fallback: got %v want %v", got, want)
	}

	// 4. nothing readable → most common (purchase month + 1) = August.
	got = resolveClosingMonth(Document{}, "", "", itemDates)
	if want := mustParseDay(t, "2026-08-01"); !got.Equal(want) {
		t.Errorf("item-date fallback: got %v want %v", got, want)
	}
}

// TestResolveClosingMonth_RegressionInvoiceDueNextMonth reproduces the production bug this
// priority order exists to prevent: the Itaú invoice due 2026-09-08 (closing 2026-08-25,
// purchases through 2026-08-25) was filed under competência 2026-08, stacking it on top of
// invoice 08 and reporting August as R$ 19.371,57 instead of R$ 9.374,78.
func TestResolveClosingMonth_RegressionInvoiceDueNextMonth(t *testing.T) {
	itemDates := []time.Time{
		mustParseDay(t, "2026-07-30"), mustParseDay(t, "2026-08-05"), mustParseDay(t, "2026-08-25"),
	}
	got := resolveClosingMonth(Document{}, "2026-08-25", "2026-09-08", itemDates)
	if want := mustParseDay(t, "2026-09-01"); !got.Equal(want) {
		t.Fatalf("an invoice due 2026-09-08 must land in competência 2026-09, got %v", got.Format("2006-01"))
	}
}

func TestReconcileVerdict(t *testing.T) {
	closing := mustParseDay(t, "2026-09-01")

	// No printed total to check against → RECONCILED, no note.
	res := reconcileVerdict("3671.48", "", closing, 63, "fatura")
	if res.Status != ExtractionReconciled || res.Note != nil {
		t.Fatalf("no target: got status=%s note=%v", res.Status, res.Note)
	}

	// Sum matches printed total within tolerance → RECONCILED.
	res = reconcileVerdict("9996.79", "9996.79", closing, 170, "fatura")
	if res.Status != ExtractionReconciled {
		t.Fatalf("balanced: got status=%s", res.Status)
	}
	if res.StatementTotal == nil || *res.StatementTotal != "9996.79" {
		t.Fatalf("balanced: statement total not carried: %v", res.StatementTotal)
	}

	// Sum short of printed total → NEEDS_INPUT with a question and the signed delta.
	res = reconcileVerdict("3671.48", "9996.79", closing, 63, "fatura")
	if res.Status != ExtractionNeedsInput {
		t.Fatalf("short: got status=%s want NEEDS_INPUT", res.Status)
	}
	if res.Delta == nil || *res.Delta != "-6325.31" {
		t.Fatalf("short: delta = %v want -6325.31", res.Delta)
	}
	if res.Note == nil || *res.Note == "" {
		t.Fatalf("short: expected a question note for the Inbox")
	}
}

// TestChunkText_RealStatementIsASinglePass locks in the design the extraction depends on: a real
// statement goes to the model as ONE complete document, not as slices. Only a single pass can read
// the header (printed total, closing/due date) and self-check its own sum against it — and each
// extra call is another chance to time out.
func TestChunkText_RealStatementIsASinglePass(t *testing.T) {
	// Comfortably larger than a 174-line invoice's text layer, and larger still than the 12k the
	// old code truncated to.
	statement := repeat("x", 45000)
	if got := chunkText(statement, statementChunkRunes, statementChunkOverlapRunes); len(got) != 1 {
		t.Fatalf("a 45k-rune statement must go to the model in one pass, got %d chunks", len(got))
	}
}

func TestChunkText(t *testing.T) {
	if got := chunkText("short", 100, 10); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short text should be one chunk unchanged, got %v", got)
	}
	text := string(make([]rune, 0)) + repeat("a", 5000)
	chunks := chunkText(text, 2000, 200)
	if len(chunks) < 3 {
		t.Fatalf("expected the 5000-rune text to split into >=3 overlapping chunks, got %d", len(chunks))
	}
	// Every rune of the source must appear in some chunk (no silent truncation).
	total := 0
	for _, c := range chunks {
		total += len([]rune(c))
	}
	if total < 5000 {
		t.Fatalf("chunks cover only %d of 5000 runes — extraction would miss part of the statement", total)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
