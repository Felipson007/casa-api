package documents

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"

	"github.com/atlas/atlas-go/internal/platform/money"
)

// Deterministic reader for Itaú credit-card statements (faturas), tried before the LLM extraction.
//
// Itaú prints the transactions in TWO columns per page whose rows are not aligned, so the plain
// text layer interleaves the two columns line by line (a purchase from the left column followed by
// one from the right) — which is what made the model return no lines at all for the 08/10/2026
// statement. This reader uses the glyph positions instead: each page is split at the gutter
// between the columns, glyphs are grouped into rows by their baseline, and the rows are read left
// column first, then right — the order Itaú lays the statement out in. Both layouts seen so far
// (until Jun/2026: "NOME (final 1234)" holder headers; from Jul/2026: holder name only, a
// "Pagamentos efetuados" block) share that structure.
//
// It is only trusted when the lines it read add up, to the cent, to the printed "Lançamentos
// atuais"; anything else returns an error and the caller falls back to the LLM path.
//
// Privacy: the statement header carries the holder's CPF and address. Nothing here logs row text.

// itauColumnGutter is the x (PDF points, A4 width 595) separating the two transaction columns:
// left-column glyphs end before ~340, right-column ones start at ~350 in every layout seen.
const itauColumnGutter = 343.0

type itauSection int

const (
	itauNone itauSection = iota
	itauPurchases
	itauInternational
	itauProducts
)

// itauStatement is what parseItauStatement read: printed dates/total and every line of the
// statement's own sections, in print order.
type itauStatement struct {
	closingDate time.Time // "Emissão" — the statement's closing day
	dueDate     time.Time
	total       string // printed "Lançamentos atuais", numeric(15,2) string
	items       []itauItem
}

type itauItem struct {
	date        time.Time
	description string
	amount      string // signed; negative for estornos/credits
	category    string // app category name, or "" when the printed one maps to nothing specific
}

var (
	// A leading icon glyph (@ virtual card, contactless, wallet) may precede the date, and the
	// newer layout glues the description to it ("04/07www.yuplay.com").
	itauDateCell     = regexp.MustCompile(`^\S{0,3}?(\d{2})/(\d{2})(.*)$`)
	itauMoneyCell    = regexp.MustCompile(`^(-\s?)?\d{1,3}(\.\d{3})*,\d{2}$`)
	itauFullDate     = regexp.MustCompile(`(\d{2}/\d{2}/\d{4})`)
	errNotItau       = errors.New("not an Itaú card statement")
	errItauUnbalance = errors.New("itaú statement lines do not add up to the printed total")
)

// itauRow is one visual line of one column: its cells are runs of glyphs separated by a visible gap.
type itauRow struct {
	y     float64
	cells []string
}

func (r itauRow) text() string { return strings.Join(r.cells, " ") }

// parseItauStatement reads an Itaú statement PDF. Returns errNotItau for anything that isn't one,
// errItauUnbalance when the lines read don't reconcile with the printed total.
func parseItauStatement(content []byte) (st itauStatement, err error) {
	defer func() {
		// ledongthuc/pdf panics on some malformed files; a reader is never worth crashing over.
		if r := recover(); r != nil {
			err = fmt.Errorf("reading itaú statement: %v", r)
		}
	}()
	reader, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return st, err
	}

	var rows []itauRow // every column-row of every page, in reading order
	for p := 1; p <= reader.NumPage(); p++ {
		page := reader.Page(p)
		if page.V.IsNull() {
			continue
		}
		var left, right []pdf.Text
		for _, g := range page.Content().Text {
			if g.X < itauColumnGutter {
				left = append(left, g)
			} else {
				right = append(right, g)
			}
		}
		rows = append(rows, itauRows(left)...)
		rows = append(rows, itauRows(right)...)
	}

	all := make([]string, 0, len(rows))
	for _, r := range rows {
		all = append(all, r.text())
	}
	joined := strings.Join(all, "\n")
	if !strings.Contains(joined, "Itaú") || !strings.Contains(joined, "Lançamentos: compras e saques") {
		return st, errNotItau
	}
	st.closingDate, _ = itauLabeledDate(all, "Emissão")
	st.dueDate, _ = itauLabeledDate(all, "Vencimento")
	if st.closingDate.IsZero() || st.dueDate.IsZero() {
		return st, fmt.Errorf("%w: closing/due date not found", errNotItau)
	}
	st.total = itauPrintedTotal(rows)
	if st.total == "" {
		return st, fmt.Errorf("%w: printed total not found", errNotItau)
	}

	section := itauNone
	var lastItem = -1 // index of the last line read, to attach its category subline
	var lastIntlDate time.Time
	for _, r := range rows {
		line := r.text()
		if s, ok := itauSectionHeading(line); ok {
			section, lastItem = s, -1
			continue
		}
		if section == itauNone {
			continue
		}
		if section == itauInternational && strings.HasPrefix(line, "Repasse de IOF") {
			if amt, ok := itauMoney(r.cells[len(r.cells)-1]); ok && !lastIntlDate.IsZero() {
				st.items = append(st.items, itauItem{date: lastIntlDate, description: "Repasse de IOF (compras internacionais)", amount: amt, category: "Tarifas e IOF"})
			}
			lastItem = -1
			continue
		}
		item, ok := itauLineItem(r, st.closingDate)
		if !ok {
			// The subline under a purchase carries Itaú's category and city.
			if lastItem >= 0 && st.items[lastItem].category == "" && section == itauPurchases {
				st.items[lastItem].category = itauCategory(line)
			}
			lastItem = -1
			continue
		}
		switch section {
		case itauInternational:
			lastIntlDate = item.date
		case itauProducts:
			item.category = "Tarifas e IOF"
		}
		if strings.HasPrefix(item.amount, "-") {
			item.category = "Ajustes e Estornos"
		}
		st.items = append(st.items, item)
		lastItem = len(st.items) - 1
	}
	if len(st.items) == 0 {
		return st, fmt.Errorf("%w: no lines read", errItauUnbalance)
	}

	sum := money.Zero
	for _, it := range st.items {
		if next, err := money.Add(sum, it.amount); err == nil {
			sum = next
		}
	}
	if !amountsWithin(sum, st.total, "0.01") {
		return st, fmt.Errorf("%w: %d lines sum %s, printed %s", errItauUnbalance, len(st.items), sum, st.total)
	}
	return st, nil
}

// itauRows groups one column's glyphs into rows (top to bottom) and each row into cells.
func itauRows(glyphs []pdf.Text) []itauRow {
	if len(glyphs) == 0 {
		return nil
	}
	sort.SliceStable(glyphs, func(i, j int) bool {
		if glyphs[i].Y != glyphs[j].Y {
			return glyphs[i].Y > glyphs[j].Y
		}
		return glyphs[i].X < glyphs[j].X
	})
	var groups [][]pdf.Text
	for _, g := range glyphs {
		n := len(groups)
		if n > 0 && groups[n-1][0].Y-g.Y <= 2.0 {
			groups[n-1] = append(groups[n-1], g)
			continue
		}
		groups = append(groups, []pdf.Text{g})
	}
	rows := make([]itauRow, 0, len(groups))
	for _, gs := range groups {
		sort.SliceStable(gs, func(i, j int) bool { return gs[i].X < gs[j].X })
		var cells []string
		var cur strings.Builder
		end := -1.0
		for _, g := range gs {
			gap := g.FontSize * 0.9
			if gap < 3 {
				gap = 3
			}
			if end >= 0 && g.X-end > gap {
				if c := strings.TrimSpace(cur.String()); c != "" {
					cells = append(cells, c)
				}
				cur.Reset()
			}
			cur.WriteString(g.S)
			end = g.X + g.W
		}
		if c := strings.TrimSpace(cur.String()); c != "" {
			cells = append(cells, c)
		}
		if len(cells) > 0 {
			rows = append(rows, itauRow{y: gs[0].Y, cells: cells})
		}
	}
	return rows
}

// itauSectionHeading recognises the headings that start or end a block of statement lines.
func itauSectionHeading(line string) (itauSection, bool) {
	l := strings.ToLower(line)
	switch {
	case strings.Contains(l, "lançamentos: compras e saques"):
		return itauPurchases, true
	case strings.Contains(l, "lançamentos internacionais"):
		return itauInternational, true
	case strings.Contains(l, "lançamentos: produtos e serviços"):
		return itauProducts, true
	case strings.Contains(l, "compras parceladas - próximas faturas"),
		strings.Contains(l, "pagamentos efetuados"),
		strings.Contains(l, "limites de crédito"),
		strings.Contains(l, "encargos cobrados nesta fatura"),
		strings.Contains(l, "fique atento aos encargos"),
		strings.Contains(l, "novo teto de juros"),
		strings.Contains(l, "simulação de compras"),
		strings.Contains(l, "simulação saque"),
		strings.Contains(l, "demais taxas de juros"),
		strings.Contains(l, "total dos lançamentos atuais"):
		return itauNone, true
	}
	return itauNone, false
}

// itauLineItem reads "dd/mm | description | [-]1.234,56". The year comes from the closing date:
// a day/month after the closing month belongs to the previous year (an installment bought in
// December billed in a January statement, or any older installment).
func itauLineItem(r itauRow, closing time.Time) (itauItem, bool) {
	cells := append([]string(nil), r.cells...)
	if len(cells) < 2 {
		return itauItem{}, false
	}
	m := itauDateCell.FindStringSubmatch(cells[0])
	if m == nil {
		return itauItem{}, false
	}
	if rest := strings.TrimSpace(m[3]); rest != "" {
		if rest[0] == '/' || (rest[0] >= '0' && rest[0] <= '9') {
			return itauItem{}, false // a full dd/mm/yyyy date or a number, not a statement line
		}
		cells = append([]string{m[1] + "/" + m[2], rest}, cells[1:]...)
	}
	if len(cells) < 3 {
		return itauItem{}, false
	}
	last := cells[len(cells)-1]
	if len(cells) >= 4 && (cells[len(cells)-2] == "-") {
		last = "-" + last
		cells = cells[:len(cells)-1]
	}
	amount, ok := itauMoney(last)
	if !ok {
		return itauItem{}, false
	}
	desc := strings.Join(strings.Fields(strings.Join(cells[1:len(cells)-1], " ")), " ")
	if desc == "" {
		return itauItem{}, false
	}
	var day, month int
	fmt.Sscanf(m[1], "%d", &day)
	fmt.Sscanf(m[2], "%d", &month)
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return itauItem{}, false
	}
	year := closing.Year()
	if time.Month(month) > closing.Month() {
		year--
	}
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if date.Month() != time.Month(month) { // 31/04 and friends
		return itauItem{}, false
	}
	return itauItem{date: date, description: desc, amount: amount}, true
}

// itauMoney turns "1.234,56" / "-1.234,56" / "- 0,01" into a signed numeric(15,2) string.
func itauMoney(cell string) (string, bool) {
	cell = strings.TrimSpace(cell)
	if !itauMoneyCell.MatchString(cell) {
		return "", false
	}
	neg := strings.HasPrefix(cell, "-")
	digits := strings.NewReplacer("-", "", " ", "", ".", "", ",", ".").Replace(cell)
	if neg {
		digits = "-" + digits
	}
	return digits, true
}

// itauLabeledDate finds "Label: dd/mm/yyyy" (first occurrence).
func itauLabeledDate(lines []string, label string) (time.Time, bool) {
	for _, l := range lines {
		i := strings.Index(l, label)
		if i < 0 {
			continue
		}
		if m := itauFullDate.FindString(l[i:]); m != "" {
			if t, err := time.Parse("02/01/2006", m); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// itauPrintedTotal is the statement's own new charges: "Total dos lançamentos atuais" (the footer
// of the line sections), or "Lançamentos atuais" from the summary box.
func itauPrintedTotal(rows []itauRow) string {
	for _, prefix := range []string{"total dos lançamentos atuais", "lançamentos atuais"} {
		for _, r := range rows {
			l := strings.ToLower(r.text())
			if !strings.Contains(l, prefix) {
				continue
			}
			if amt, ok := itauMoney(r.cells[len(r.cells)-1]); ok {
				return amt
			}
		}
	}
	return ""
}

// itauCategory maps the category Itaú prints under a purchase ("ALIMENTAÇÃO .BARUERI",
// "transporte  SAO PAULO") onto the app's categories. Generic ones (diversos/outros/serviços) map
// to "" so the household's own category memory decides.
func itauCategory(subline string) string {
	l := strings.ToLower(subline)
	for _, c := range []struct{ prefix, category string }{
		{"alimenta", "Alimentação"}, {"restaurante", "Alimentação"}, {"supermercado", "Alimentação"},
		{"veículos", "Transporte"}, {"veiculos", "Transporte"}, {"transporte", "Transporte"},
		{"saúde", "Saúde"}, {"saude", "Saúde"},
		{"vestuário", "Compras"}, {"vestuario", "Compras"}, {"eletronicos", "Compras"}, {"eletrônicos", "Compras"},
		{"hobby", "Lazer"}, {"turismo", "Lazer"}, {"lazer", "Lazer"}, {"viagem", "Lazer"},
		{"educação", "Educação"}, {"educacao", "Educação"},
		{"moradia", "Moradia"},
	} {
		if strings.HasPrefix(l, c.prefix) {
			return c.category
		}
	}
	return ""
}

// invoiceItems converts the statement into the extraction pipeline's line type.
func (st itauStatement) invoiceItems() []parsedInvoiceItem {
	out := make([]parsedInvoiceItem, 0, len(st.items))
	for _, it := range st.items {
		out = append(out, parsedInvoiceItem{amount: it.amount, date: it.date, description: it.description, category: it.category})
	}
	return out
}
