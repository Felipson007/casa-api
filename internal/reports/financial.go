package reports

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	"github.com/go-pdf/fpdf"

	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/jobs"
	"github.com/atlas/atlas-go/internal/platform/money"
)

// processFinancialReport mirrors RelatorioFinanceiroService.gerar. yearStart/yearEnd come from the
// job's payload (set at RequestFinancialReport time).
func (s *Service) processFinancialReport(ctx context.Context, job jobs.Job) (map[string]any, error) {
	yearStart := payloadInt(job.Payload, "yearStart")
	yearEnd := payloadInt(job.Payload, "yearEnd")
	if yearEnd == 0 {
		yearEnd = yearStart
	}

	expenseSummary, err := s.expenses.RangeSummary(ctx, yearStart, yearEnd)
	if err != nil {
		return nil, fmt.Errorf("reports: summarizing expenses: %w", err)
	}
	incomeSummary, err := s.income.RangeSummary(ctx, yearStart, yearEnd)
	if err != nil {
		return nil, fmt.Errorf("reports: summarizing income: %w", err)
	}
	saldo, err := money.Sub(incomeSummary.NetTotal, expenseSummary.TotalAmount)
	if err != nil {
		return nil, fmt.Errorf("reports: computing period balance: %w", err)
	}

	companyName, _ := companyBranding(s.settings, ctx)
	titulo := strconv.Itoa(yearStart)
	if yearEnd != yearStart {
		titulo = fmt.Sprintf("%d–%d", yearStart, yearEnd)
	}

	pdf, err := buildFinancialReportPDF(financialData{
		companyName: companyName, periodTitle: titulo,
		totalExpense: expenseSummary.TotalAmount, totalIncome: incomeSummary.NetTotal, balance: saldo,
		byYear:            mergeYears(expenseSummary.ByYear, incomeSummary.ByYear),
		expenseByCategory: expenseSummary.ByCategory, incomeByCategory: incomeSummary.ByCategory,
	})
	if err != nil {
		return nil, err
	}

	path, err := s.storage.Write(job.ID, "pdf", pdf)
	if err != nil {
		return nil, err
	}
	return map[string]any{"filePath": path, "sizeBytes": len(pdf)}, nil
}

type yearRow struct {
	year         int
	totalExpense string
	totalIncome  string
	balance      string
}

func mergeYears(expenseYears []expenses.YearSummaryDTO, incomeYears []income.YearSummaryDTO) []yearRow {
	incomeByYear := make(map[int]string, len(incomeYears))
	for _, y := range incomeYears {
		incomeByYear[y.Year] = y.Total
	}
	rows := make([]yearRow, 0, len(expenseYears))
	for _, e := range expenseYears {
		inc := incomeByYear[e.Year]
		if inc == "" {
			inc = money.Zero
		}
		balance := addMoneySafe(inc, "-"+e.Total) // Sub isn't imported here; add the negation directly
		rows = append(rows, yearRow{year: e.Year, totalExpense: e.Total, totalIncome: inc, balance: balance})
	}
	return rows
}

type financialData struct {
	companyName       string
	periodTitle       string
	totalExpense      string
	totalIncome       string
	balance           string
	byYear            []yearRow
	expenseByCategory []expenses.CategorySummaryDTO
	incomeByCategory  []income.CategorySummaryDTO
}

func buildFinancialReportPDF(d financialData) ([]byte, error) {
	pdf, tr := newReportPDF()

	writeReportHeader(pdf, tr, d.companyName, "RELATÓRIO FINANCEIRO — "+d.periodTitle)
	pdf.Ln(4)

	highlightWidth := pageWidth / 3
	writeHighlightBox(pdf, tr, highlightWidth, "Receitas (líquido)", "R$ "+formatBRL(d.totalIncome), [3]int{220, 240, 220})
	writeHighlightBox(pdf, tr, highlightWidth, "Gastos", "R$ "+formatBRL(d.totalExpense), [3]int{250, 225, 225})
	writeHighlightBox(pdf, tr, highlightWidth, "Saldo do período", "R$ "+formatBRL(d.balance), [3]int{220, 230, 250})
	pdf.Ln(10)

	if len(d.byYear) > 1 {
		writeSectionTitle(pdf, tr, "Por ano", 11, 7)
		widths := []float64{35, 45, 45, 45}
		writeTableHeaderRow(pdf, tr, widths, []string{"Ano", "Receitas", "Gastos", "Saldo"})
		for _, y := range d.byYear {
			pdf.CellFormat(widths[0], 5.5, strconv.Itoa(y.year), "1", 0, "C", false, 0, "")
			pdf.CellFormat(widths[1], 5.5, "R$ "+formatBRL(y.totalIncome), "1", 0, "R", false, 0, "")
			pdf.CellFormat(widths[2], 5.5, "R$ "+formatBRL(y.totalExpense), "1", 0, "R", false, 0, "")
			pdf.CellFormat(widths[3], 5.5, "R$ "+formatBRL(y.balance), "1", 0, "R", false, 0, "")
			pdf.Ln(-1)
		}
		pdf.Ln(4)
	}

	if len(d.expenseByCategory) > 0 {
		writeSectionTitle(pdf, tr, "Gastos por categoria", 11, 7)
		writeCategoryTable(pdf, tr, categoriesFromExpenses(d.expenseByCategory))
		pdf.Ln(4)
	}
	if len(d.incomeByCategory) > 0 {
		writeSectionTitle(pdf, tr, "Receitas por categoria", 11, 7)
		writeCategoryTable(pdf, tr, categoriesFromIncome(d.incomeByCategory))
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("reports: rendering financial report pdf: %w", err)
	}
	return buf.Bytes(), nil
}

type categoryRow struct {
	name  string
	total string
}

func categoriesFromExpenses(rows []expenses.CategorySummaryDTO) []categoryRow {
	out := make([]categoryRow, len(rows))
	for i, r := range rows {
		out[i] = categoryRow{name: r.CategoryName, total: r.Total}
	}
	return out
}

func categoriesFromIncome(rows []income.CategorySummaryDTO) []categoryRow {
	out := make([]categoryRow, len(rows))
	for i, r := range rows {
		out[i] = categoryRow{name: r.CategoryName, total: r.Total}
	}
	return out
}

func writeCategoryTable(pdf *fpdf.Fpdf, tr func(string) string, rows []categoryRow) {
	widths := []float64{120, 60}
	writeTableHeaderRow(pdf, tr, widths, []string{"Categoria", "Total"})
	for _, r := range rows {
		pdf.CellFormat(widths[0], 5.5, tr(r.name), "1", 0, "L", false, 0, "")
		pdf.CellFormat(widths[1], 5.5, "R$ "+formatBRL(r.total), "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}
}

func writeHighlightBox(pdf *fpdf.Fpdf, tr func(string) string, width float64, label, value string, color [3]int) {
	x, y := pdf.GetXY()
	pdf.SetFillColor(color[0], color[1], color[2])
	pdf.RoundedRect(x, y, width-4, 20, 2, "1234", "F")
	pdf.SetXY(x+3, y+3)
	pdf.SetFont("Arial", "", 8)
	pdf.CellFormat(width-10, 5, tr(label), "", 2, "L", false, 0, "")
	pdf.SetXY(x+3, y+10)
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(width-10, 7, tr(value), "", 0, "L", false, 0, "")
	pdf.SetXY(x+width, y)
}
