package reports

import (
	"bytes"
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/atlas/atlas-go/internal/jobs"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/atlas/atlas-go/internal/platform/money"
)

// spreadsheetMonthsBack is a deliberate scope reduction from Java's PlanilhaExportService, which
// auto-detects the full historical range from the earliest/latest installment due date in the
// whole database (could be years of sheets). That would need a new min/max-due-date repository
// method across the stack; exporting the trailing 12 months (the practically useful range for a
// personal finance tool) avoids that without losing the feature's real value.
const spreadsheetMonthsBack = 12

// processSpreadsheetExport mirrors PlanilhaExportService.exportar + PlanilhaSheetWriter.gerar —
// one sheet per month, each with a KPI block and a debtors table.
func (s *Service) processSpreadsheetExport(ctx context.Context, job jobs.Job) (map[string]any, error) {
	now := civildate.Today().Time()
	current := loans.YearMonth{Year: now.Year(), Month: int(now.Month())}
	months := make([]loans.YearMonth, spreadsheetMonthsBack)
	for i := range months {
		months[i] = current.AddMonths(-(spreadsheetMonthsBack - 1 - i))
	}

	kpisByMonth, err := s.loans.CalculateKPIs(ctx, months)
	if err != nil {
		return nil, fmt.Errorf("reports: computing kpis for spreadsheet: %w", err)
	}
	vigentesByMonth, err := s.loans.Vigentes(ctx, months)
	if err != nil {
		return nil, fmt.Errorf("reports: computing vigentes for spreadsheet: %w", err)
	}
	installmentAmountByLoan, err := s.installmentAmountsByLoanAndMonth(ctx, vigentesByMonth)
	if err != nil {
		return nil, err
	}
	amortizationByLoanMonth, err := s.amortizationsByLoanAndMonth(ctx)
	if err != nil {
		return nil, err
	}

	companyName, _ := companyBranding(s.settings, ctx)

	f := excelize.NewFile()
	defer f.Close()
	firstSheet := f.GetSheetName(0)
	for i, m := range months {
		sheetName := fmt.Sprintf("%s-%02d", monthAbbrevPt3(m.Month), m.Year%100)
		if _, err := f.NewSheet(sheetName); err != nil {
			return nil, fmt.Errorf("reports: creating sheet for %v: %w", m, err)
		}
		if i == 0 {
			_ = f.DeleteSheet(firstSheet)
		}
		if err := writeSpreadsheetMonth(f, sheetName, companyName, m, kpisByMonth[m], vigentesByMonth[m], installmentAmountByLoan, amortizationByLoanMonth[m]); err != nil {
			return nil, fmt.Errorf("reports: writing sheet for %v: %w", m, err)
		}
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, fmt.Errorf("reports: serializing spreadsheet: %w", err)
	}

	path, err := s.storage.Write(job.ID, "xlsx", buf.Bytes())
	if err != nil {
		return nil, err
	}
	return map[string]any{"filePath": path, "sizeBytes": buf.Len()}, nil
}

// installmentAmountsByLoanAndMonth fetches each distinct loan's installments once (not once per
// month it appears in) and indexes InstallmentAmount by (loanID, month) — O(distinct loans) round
// trips for the whole export, not O(loans × months).
func (s *Service) installmentAmountsByLoanAndMonth(ctx context.Context, vigentesByMonth map[loans.YearMonth][]loans.LoanSummaryDTO) (map[uuid.UUID]map[loans.YearMonth]string, error) {
	seen := make(map[uuid.UUID]bool)
	result := make(map[uuid.UUID]map[loans.YearMonth]string)
	for _, rows := range vigentesByMonth {
		for _, r := range rows {
			if seen[r.LoanID] {
				continue
			}
			seen[r.LoanID] = true
			insts, err := s.loans.ListInstallments(ctx, r.LoanID)
			if err != nil {
				return nil, fmt.Errorf("reports: listing installments for loan %s: %w", r.LoanID, err)
			}
			byMonth := make(map[loans.YearMonth]string, len(insts))
			for _, inst := range insts {
				m := loans.YearMonth{Year: inst.DueDate.Time().Year(), Month: int(inst.DueDate.Time().Month())}
				byMonth[m] = inst.InstallmentAmount
			}
			result[r.LoanID] = byMonth
		}
	}
	return result, nil
}

// amortizationsByLoanAndMonth sums AMORTIZATION transactions per (loan, month) in a single
// ListAllTransactions(nil) round trip across every client — O(1) round trip, O(n) in-memory
// grouping.
func (s *Service) amortizationsByLoanAndMonth(ctx context.Context) (map[loans.YearMonth]map[uuid.UUID]string, error) {
	txs, err := s.loans.ListAllTransactions(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reports: listing all transactions for spreadsheet: %w", err)
	}
	result := make(map[loans.YearMonth]map[uuid.UUID]string)
	for _, t := range txs {
		if t.Type != loans.TransactionTypeAmortization || t.LoanID == nil {
			continue
		}
		m := loans.YearMonth{Year: t.PaymentDate.Time().Year(), Month: int(t.PaymentDate.Time().Month())}
		if result[m] == nil {
			result[m] = make(map[uuid.UUID]string)
		}
		existing := result[m][*t.LoanID]
		if existing == "" {
			existing = money.Zero
		}
		result[m][*t.LoanID] = addMoneySafe(existing, t.Amount)
	}
	return result, nil
}

var monthStatusLabel = map[loans.MonthStatus]string{
	loans.MonthStatusPaid:    "✅ PAGO",
	loans.MonthStatusPending: "⏳ PENDENTE",
	loans.MonthStatusOverdue: "🔴 ATRASADO",
	loans.MonthStatusPaidOff: "✅ QUITADO",
}

func writeSpreadsheetMonth(
	f *excelize.File, sheet, companyName string, m loans.YearMonth, kpis loans.KPIsDTO,
	debtorLoans []loans.LoanSummaryDTO, installmentAmountByLoan map[uuid.UUID]map[loans.YearMonth]string,
	amortByLoan map[uuid.UUID]string,
) error {
	titleStyle, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	if err != nil {
		return err
	}
	headerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true}, Fill: excelize.Fill{Type: "pattern", Color: []string{"E0E0E0"}, Pattern: 1},
	})
	if err != nil {
		return err
	}
	currencyStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: strPtr(`"R$" #,##0.00`)})
	if err != nil {
		return err
	}
	percentStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: strPtr(`0.00"%"`)})
	if err != nil {
		return err
	}

	_ = f.SetCellValue(sheet, "A1", "💰 CONTROLE DE EMPRÉSTIMOS — "+companyName)
	_ = f.SetCellStyle(sheet, "A1", "A1", titleStyle)
	_ = f.SetCellValue(sheet, "A3", "📅 MÊS: "+monthLabel(m.Year, m.Month))

	_ = f.SetSheetRow(sheet, "A5", &[]any{
		"Capital em risco", "Esperado no mês", "Recebido no mês", "Amortizações no mês", "Total no mês", "Empréstimos ativos", "Taxa média",
	})
	_ = f.SetCellStyle(sheet, "A5", "G5", headerStyle)
	kpiValues, err := parseKPIValues(kpis)
	if err != nil {
		return err
	}
	if err := f.SetSheetRow(sheet, "A6", &kpiValues); err != nil {
		return err
	}
	_ = f.SetCellStyle(sheet, "A6", "E6", currencyStyle)
	_ = f.SetCellStyle(sheet, "G6", "G6", percentStyle)

	headerRow := 8
	_ = f.SetCellValue(sheet, cellRef(1, headerRow), "📋 DEVEDORES — "+monthLabel(m.Year, m.Month))
	columnHeaders := []string{"Devedor / Empréstimo", "Capital (R$)", "Taxa", "Mensalidade (R$)", "Vencimento", "Recebido (R$)", "Amortização (R$)", "Status", "Dif. (R$)"}
	headerRow++
	_ = f.SetSheetRow(sheet, cellRef(1, headerRow), &columnHeaders)
	lastCol := len(columnHeaders)
	_ = f.SetCellStyle(sheet, cellRef(1, headerRow), cellRef(lastCol, headerRow), headerStyle)

	row := headerRow + 1
	firstDataRow := row
	for _, l := range debtorLoans {
		mensalidade := installmentAmountByLoan[l.LoanID][m]
		amort := amortByLoan[l.LoanID]
		if amort == "" {
			amort = money.Zero
		}
		diff, _ := money.Sub(l.AmountPaidThisMonth, orDefault(mensalidade, money.Zero))
		due := ""
		if l.InstallmentDueDate != nil {
			due = fmt.Sprintf("Dia %d", l.InstallmentDueDate.Time().Day())
		}
		values := []any{
			l.ClientName, mustFloat(l.PrincipalAmount), mustFloat(l.MonthlyInterestRate),
			mustFloat(orDefault(mensalidade, money.Zero)), due, mustFloat(l.AmountPaidThisMonth),
			mustFloat(amort), monthStatusLabel[l.Status], mustFloat(diff),
		}
		if err := f.SetSheetRow(sheet, cellRef(1, row), &values); err != nil {
			return err
		}
		_ = f.SetCellStyle(sheet, cellRef(2, row), cellRef(2, row), currencyStyle)
		_ = f.SetCellStyle(sheet, cellRef(3, row), cellRef(3, row), percentStyle)
		_ = f.SetCellStyle(sheet, cellRef(4, row), cellRef(4, row), currencyStyle)
		_ = f.SetCellStyle(sheet, cellRef(6, row), cellRef(6, row), currencyStyle)
		_ = f.SetCellStyle(sheet, cellRef(7, row), cellRef(7, row), currencyStyle)
		_ = f.SetCellStyle(sheet, cellRef(9, row), cellRef(9, row), currencyStyle)
		row++
	}

	if row > firstDataRow {
		_ = f.AutoFilter(sheet, fmt.Sprintf("%s:%s", cellRef(1, headerRow), cellRef(lastCol, row-1)), nil)
	}

	widths := []float64{26, 15, 12, 17, 14, 17, 17, 15, 14}
	for i, w := range widths {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetColWidth(sheet, col, col, w)
	}
	_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, Split: false, XSplit: 0, YSplit: 6, TopLeftCell: "A7", ActivePane: "bottomLeft"})
	return nil
}

func cellRef(col, row int) string {
	name, _ := excelize.ColumnNumberToName(col)
	return fmt.Sprintf("%s%d", name, row)
}

func strPtr(s string) *string { return &s }

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func mustFloat(s string) float64 {
	r, err := money.Parse(s)
	if err != nil {
		return 0
	}
	f, _ := r.Float64()
	return f
}

func parseKPIValues(k loans.KPIsDTO) ([]any, error) {
	return []any{
		mustFloat(k.CapitalAtRisk), mustFloat(k.ExpectedThisMonth), mustFloat(k.ReceivedThisMonth),
		mustFloat(k.AmortizationsThisMonth), mustFloat(k.TotalThisMonth), k.ActiveThisMonth, mustFloat(k.AverageRate),
	}, nil
}
