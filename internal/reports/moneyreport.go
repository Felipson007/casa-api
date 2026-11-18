package reports

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// moneyReportRow is the common shape expenses.ExpenseDTO and income.EntryDTO both reduce to for
// PDF rendering — the two reports are structurally identical (date/amount/category/person/
// description), so the filtering, sorting and table-drawing logic is shared instead of duplicated
// per module.
type moneyReportRow struct {
	Date        time.Time
	Amount      string
	CategoryID  *uuid.UUID
	PersonID    *uuid.UUID
	Description *string
}

// GenerateExpenseReportPDF mirrors the old GastoRelatorioController — a synchronous, single-module
// PDF of every expense in a given year, optionally narrowed to one month. This is deliberately
// separate from RequestFinancialReport (the async combined expense+income job over a year range):
// that one is a multi-year portfolio export, this is the quick per-module report the frontend's
// "Baixar relatório" button on the Gastos tab expects.
func (s *Service) GenerateExpenseReportPDF(ctx context.Context, year int, month *int) (DownloadedReport, error) {
	list, err := s.expenses.List(ctx, nil, nil, "")
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: listing expenses for report: %w", err)
	}
	rows := make([]moneyReportRow, 0, len(list))
	for _, e := range list {
		rows = append(rows, moneyReportRow{
			Date: e.ExpenseDate.Time(), Amount: e.Amount,
			CategoryID: e.CategoryID, PersonID: e.PersonID, Description: e.Description,
		})
	}
	return s.buildMoneyReportPDF(ctx, "GASTOS", "gastos", year, month, rows)
}

// GenerateIncomeReportPDF is GenerateExpenseReportPDF's mirror for /api/income/report — see its
// doc comment.
func (s *Service) GenerateIncomeReportPDF(ctx context.Context, year int, month *int) (DownloadedReport, error) {
	list, err := s.income.List(ctx, nil, nil, "")
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: listing income for report: %w", err)
	}
	rows := make([]moneyReportRow, 0, len(list))
	for _, e := range list {
		rows = append(rows, moneyReportRow{
			Date: e.IncomeDate.Time(), Amount: e.Amount,
			CategoryID: e.CategoryID, PersonID: e.PersonID, Description: e.Description,
		})
	}
	return s.buildMoneyReportPDF(ctx, "RECEITAS", "receitas", year, month, rows)
}

func (s *Service) buildMoneyReportPDF(ctx context.Context, titleWord, fileWord string, year int, month *int, all []moneyReportRow) (DownloadedReport, error) {
	if month != nil && (*month < 1 || *month > 12) {
		return DownloadedReport{}, apperr.Validation("month must be between 1 and 12", "month")
	}

	rows := make([]moneyReportRow, 0, len(all))
	for _, r := range all {
		if r.Date.Year() != year {
			continue
		}
		if month != nil && int(r.Date.Month()) != *month {
			continue
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Date.Before(rows[j].Date) })

	categoryNames, err := s.categoryNameLookup(ctx)
	if err != nil {
		return DownloadedReport{}, err
	}
	personNames, err := s.personNameLookup(ctx)
	if err != nil {
		return DownloadedReport{}, err
	}

	title := fmt.Sprintf("%s — %d", titleWord, year)
	if month != nil {
		title = titleWord + " — " + monthLabel(year, *month)
	}

	companyName, _ := companyBranding(s.settings, ctx)
	pdfBytes, err := buildMoneyReportPDF(companyName, title, rows, categoryNames, personNames)
	if err != nil {
		return DownloadedReport{}, err
	}

	filename := fmt.Sprintf("relatorio-%s-%d", fileWord, year)
	if month != nil {
		filename = fmt.Sprintf("relatorio-%s-%d-%02d", fileWord, year, *month)
	}
	return DownloadedReport{
		Content: pdfBytes, ContentType: "application/pdf",
		Filename: filename + ".pdf",
	}, nil
}

func (s *Service) categoryNameLookup(ctx context.Context) (map[uuid.UUID]string, error) {
	list, err := s.categories.List(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reports: listing categories for report: %w", err)
	}
	out := make(map[uuid.UUID]string, len(list))
	for _, c := range list {
		out[c.ID] = c.Name
	}
	return out, nil
}

func (s *Service) personNameLookup(ctx context.Context) (map[uuid.UUID]string, error) {
	list, err := s.people.ListPeople(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("reports: listing people for report: %w", err)
	}
	out := make(map[uuid.UUID]string, len(list))
	for _, p := range list {
		out[p.ID] = p.Name
	}
	return out, nil
}

func buildMoneyReportPDF(companyName, title string, rows []moneyReportRow, categoryNames, personNames map[uuid.UUID]string) ([]byte, error) {
	pdf, tr := newReportPDF()

	writeReportHeader(pdf, tr, companyName, title)
	pdf.Ln(4)

	total := "0.00"
	for _, r := range rows {
		total = addMoneySafe(total, r.Amount)
	}
	pdf.SetFillColor(brandTintRGB[0], brandTintRGB[1], brandTintRGB[2])
	pdf.SetFont("Arial", "B", 11)
	pdf.SetTextColor(brandDarkRGB[0], brandDarkRGB[1], brandDarkRGB[2])
	pdf.CellFormat(0, 9, tr(fmt.Sprintf("Total: R$ %s  (%d lançamentos)", formatBRL(total), len(rows))), "", 1, "L", true, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.Ln(3)

	if len(rows) == 0 {
		pdf.SetFont("Arial", "", 9)
		pdf.CellFormat(0, 6, tr("Nenhum lançamento neste período."), "", 1, "L", false, 0, "")
	} else {
		widths := []float64{25, 40, 35, 35, 45}
		writeTableHeaderRow(pdf, tr, widths, []string{"Data", "Categoria", "Pessoa", "Valor", "Descrição"})
		for i, r := range rows {
			category := "—"
			if r.CategoryID != nil {
				if name, ok := categoryNames[*r.CategoryID]; ok {
					category = name
				}
			}
			person := "—"
			if r.PersonID != nil {
				if name, ok := personNames[*r.PersonID]; ok {
					person = name
				}
			}
			desc := ""
			if r.Description != nil {
				desc = *r.Description
			}
			// Zebra striping: a long transaction list is otherwise a wall of identical white rows,
			// hard to track across columns by eye — alternating a near-invisible tint every other
			// row is the standard fix real statement software uses.
			stripe := i%2 == 1
			if stripe {
				pdf.SetFillColor(rowStripeRGB[0], rowStripeRGB[1], rowStripeRGB[2])
			}
			pdf.CellFormat(widths[0], 5.5, r.Date.Format("02/01/2006"), "1", 0, "C", stripe, 0, "")
			pdf.CellFormat(widths[1], 5.5, tr(category), "1", 0, "L", stripe, 0, "")
			pdf.CellFormat(widths[2], 5.5, tr(person), "1", 0, "L", stripe, 0, "")
			pdf.CellFormat(widths[3], 5.5, "R$ "+formatBRL(r.Amount), "1", 0, "R", stripe, 0, "")
			pdf.CellFormat(widths[4], 5.5, tr(desc), "1", 0, "L", stripe, 0, "")
			pdf.Ln(-1)
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("reports: rendering money report pdf: %w", err)
	}
	return buf.Bytes(), nil
}
