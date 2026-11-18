package reports

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"
)

var monthNamesPt = [...]string{
	"Janeiro", "Fevereiro", "Março", "Abril", "Maio", "Junho",
	"Julho", "Agosto", "Setembro", "Outubro", "Novembro", "Dezembro",
}

// Palette mirrors the frontend's --brand token (app/globals.css, #00a67d) so an exported PDF
// reads as the same product's letterhead instead of a generic system report. Kept as package-level
// [3]int RGB triples — fpdf's SetFillColor/SetTextColor/SetDrawColor all take three ints, no color
// type of its own.
var (
	brandRGB     = [3]int{0, 166, 125}   // #00a67d
	brandDarkRGB = [3]int{0, 92, 70}     // header-band title text needs no contrast help (white on
	mutedRGB     = [3]int{120, 120, 120} // brand), but a darker tone reads better as body-text accents
	brandTintRGB = [3]int{224, 246, 240} // pale brand tint for highlight cards/rows
	rowStripeRGB = [3]int{244, 244, 244} // zebra-striping on long tables, close to bg so it's a hint
	overdueRGB   = [3]int{250, 210, 210}
	paidOffRGB   = [3]int{200, 230, 200}
)

// newReportPDF centralizes the setup every report builder repeated identically (A4 portrait,
// cp1252 translator for PT-BR accents, 15mm margins, first page) and adds a "Página X de Y" footer
// on every page via fpdf's alias mechanism — the one polish item that's easiest to get once, here,
// rather than per builder.
func newReportPDF() (*fpdf.Fpdf, func(string) string) {
	pdf := fpdf.New("P", "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("cp1252")
	pdf.SetMargins(15, 15, 15)
	pdf.AliasNbPages("")
	pdf.SetFooterFunc(func() {
		pdf.SetY(-15)
		pdf.SetDrawColor(rowStripeRGB[0], rowStripeRGB[1], rowStripeRGB[2])
		pdf.Line(15, pdf.GetY(), 195, pdf.GetY())
		pdf.SetFont("Arial", "", 8)
		pdf.SetTextColor(mutedRGB[0], mutedRGB[1], mutedRGB[2])
		pdf.CellFormat(0, 10, tr(fmt.Sprintf("Página %d de {nb}", pdf.PageNo())), "", 0, "C", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	})
	pdf.AddPage()
	return pdf, tr
}

// writeSectionTitle draws a bold section heading in the brand's darker accent tone (not full
// brand-color, which is too saturated at text size — the tint reads as an accent, not a warning)
// instead of plain black, so a page's structure ("Resumo geral", "Movimentações", "Empréstimo #2")
// is scannable at a glance. fontSize/height are explicit, not derived, so each call site keeps the
// exact line height it already had (headings appear at a few different sizes across report types).
func writeSectionTitle(pdf *fpdf.Fpdf, tr func(string) string, text string, fontSize, height float64) {
	pdf.SetFont("Arial", "B", fontSize)
	pdf.SetTextColor(brandDarkRGB[0], brandDarkRGB[1], brandDarkRGB[2])
	pdf.CellFormat(0, height, tr(text), "", 1, "L", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
}

// writeTableHeaderRow draws one header row in the brand color with white bold text — the shared
// look every report's tables now use, replacing what used to be a plain gray CellFormat block
// hand-repeated (with slightly different args) in every single builder. Always center-aligned:
// every existing call site already used "C" for every header cell, so this doesn't lose anything.
func writeTableHeaderRow(pdf *fpdf.Fpdf, tr func(string) string, widths []float64, headers []string) {
	pdf.SetFont("Arial", "B", 9)
	pdf.SetFillColor(brandRGB[0], brandRGB[1], brandRGB[2])
	pdf.SetTextColor(255, 255, 255)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 7, tr(h), "1", 0, "C", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Arial", "", 8)
}

// monthLabel mirrors the old PDF's "Julho/2026" month header — month is 1-12.
func monthLabel(year, month int) string {
	return monthNamesPt[month-1] + "/" + strconv.Itoa(year)
}

// monthAbbrevPt3 mirrors PlanilhaSheetWriter's 3-letter sheet-name month abbreviation.
func monthAbbrevPt3(month int) string {
	return monthNamesPt[month-1][:3]
}

// formatBRL renders a numeric(15,2)-shaped string ("1234.56") as "1.234,56" (no currency symbol —
// callers that want "R$" prefix it themselves, since some layouts need the bare number for a
// column that's already labeled).
func formatBRL(amount string) string {
	neg := strings.HasPrefix(amount, "-")
	amount = strings.TrimPrefix(amount, "-")
	whole, frac, ok := strings.Cut(amount, ".")
	if !ok {
		frac = "00"
	}
	if len(frac) < 2 {
		frac += strings.Repeat("0", 2-len(frac))
	}
	frac = frac[:2]

	var grouped strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			grouped.WriteByte('.')
		}
		grouped.WriteRune(r)
	}

	result := grouped.String() + "," + frac
	if neg {
		result = "-" + result
	}
	return result
}

// formatBRLCurrency is formatBRL with the "R$ " prefix — used for standalone highlight values.
func formatBRLCurrency(amount string) string {
	return "R$ " + formatBRL(amount)
}

// formatPercentBR renders a numeric(12,6)-shaped rate string ("2.500000" — extra precision for
// compound-interest calculations, never for display) as "2,50" — exactly mirrors the frontend's
// formatPercentBR (lib/format.ts): always exactly 2 decimals, comma separator. Callers append the
// "%" themselves, same convention as formatBRL not including "R$". Falls back to the raw string on
// a parse failure rather than dropping the value — a wrong-looking number beats a missing one.
func formatPercentBR(rate string) string {
	f, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return rate
	}
	return formatFloatBR(f, 2)
}

// formatFloatBR renders a plain float64 (not a DB decimal string) with a fixed number of decimals
// and a comma separator — for values computed in Go itself (e.g. loanpdf.go's percentualQuitado),
// where strconv.FormatFloat's default "." would otherwise leak through as-is.
func formatFloatBR(f float64, decimals int) string {
	return strings.Replace(strconv.FormatFloat(f, 'f', decimals, 64), ".", ",", 1)
}

// effectiveMonthlyRateBR derives the interest rate actually applied for one period from that
// period's frozen figures (interest / balance), not from the loan's current monthly_interest_rate
// — a loan whose rate was renegotiated shows its old rate on the months billed before the change.
// Returns "—" when the balance is zero or either value doesn't parse. Callers append nothing;
// the "%" is included.
func effectiveMonthlyRateBR(interest, balance string) string {
	i, errI := strconv.ParseFloat(interest, 64)
	b, errB := strconv.ParseFloat(balance, 64)
	if errI != nil || errB != nil || b == 0 {
		return "—"
	}
	return formatFloatBR(i/b*100, 2) + "%"
}
