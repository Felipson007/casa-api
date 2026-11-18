package reports

import (
	"bytes"
	"context"
	"fmt"

	"github.com/go-pdf/fpdf"

	"github.com/atlas/atlas-go/internal/clients"
	"github.com/atlas/atlas-go/internal/loans"
)

// GenerateLoanPreviewPDF mirrors EmprestimoSimulacaoService.gerarSimulacaoPdf — a synchronous,
// non-persisting PDF preview of a not-yet-saved loan's payment schedule, built from the exact same
// request shape as POST /api/loans (loans.CreateLoanRequest). Reuses
// loans.GenerateFixedSchedule/GenerateRecurringChargesThroughYearEnd directly — the same pure
// schedule math loans.Service.Create itself calls at contract time — so the preview and the real
// saved schedule can never drift apart, and nothing is persisted here.
func (s *Service) GenerateLoanPreviewPDF(ctx context.Context, req loans.CreateLoanRequest) (DownloadedReport, error) {
	client, err := s.clients.Get(ctx, req.ClientID)
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: loading client for loan preview: %w", err)
	}

	monthsToFirst := 1
	if req.SkipFirstMonth {
		monthsToFirst = 2
	}

	var calcs []loans.InstallmentCalc
	switch req.PaymentType {
	case loans.PaymentTypeInstallment:
		calcs, err = loans.GenerateFixedSchedule(req.PrincipalAmount, req.MonthlyInterestRate, req.InstallmentCount, req.ContractDate.Time(), req.DueDay, monthsToFirst)
	case loans.PaymentTypeRecurringInterest:
		calcs, err = loans.GenerateRecurringChargesThroughYearEnd(req.PrincipalAmount, req.MonthlyInterestRate, req.ContractDate.Time(), req.DueDay, monthsToFirst)
		calcs = firstProjectedCharge(calcs)
	}
	if err != nil {
		return DownloadedReport{}, err
	}

	companyName, _ := companyBranding(s.settings, ctx)
	pdfBytes, err := buildLoanPreviewPDF(companyName, client, req, calcs)
	if err != nil {
		return DownloadedReport{}, err
	}
	return DownloadedReport{
		Content: pdfBytes, ContentType: "application/pdf",
		Filename: "simulacao-emprestimo.pdf",
	}, nil
}

// firstProjectedCharge reduces a recurring-interest loan's full first-year batch to just its first
// row — mirrors EmprestimoSimulacaoService's own .subList(0,1): a full year of identical monthly
// interest rows would be noise in a preview, not useful information. A no-op for 0 or 1 rows.
func firstProjectedCharge(calcs []loans.InstallmentCalc) []loans.InstallmentCalc {
	if len(calcs) > 1 {
		return calcs[:1]
	}
	return calcs
}

func buildLoanPreviewPDF(companyName string, client clients.ClientDTO, req loans.CreateLoanRequest, calcs []loans.InstallmentCalc) ([]byte, error) {
	pdf, tr := newReportPDF()

	writeReportHeader(pdf, tr, companyName, "SIMULAÇÃO DE EMPRÉSTIMO")
	pdf.Ln(4)
	writeClientCard(pdf, tr, client)

	writeSectionTitle(pdf, tr, "Dados da simulação", 10, 6)
	pdf.SetFont("Arial", "", 9)
	pdf.CellFormat(pageWidth/2, 5, tr("Data do contrato: "+req.ContractDate.Time().Format("02/01/2006")), "", 0, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Dia de vencimento: %d", req.DueDay)), "", 1, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Valor principal: R$ %s", formatBRL(req.PrincipalAmount))), "", 0, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Taxa mensal: %s%%", formatPercentBR(req.MonthlyInterestRate))), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 5, tr("Tipo: "+paymentTypeLabel[req.PaymentType]), "", 1, "L", false, 0, "")
	pdf.Ln(3)

	if req.PaymentType == loans.PaymentTypeRecurringInterest {
		writeSectionTitle(pdf, tr, "Primeira cobrança projetada", 10, 6)
		writeInstallmentCalcTable(pdf, tr, calcs)
		pdf.Ln(2)
		pdf.SetFont("Arial", "", 8)
		pdf.MultiCell(0, 4.5, tr("Este é um empréstimo somente-juros: o valor acima se repete mensalmente "+
			"sem reduzir o principal, até que uma amortização seja registrada. Valores sujeitos a "+
			"confirmação no momento da contratação."), "", "L", false)
	} else {
		writeSectionTitle(pdf, tr, "Parcelas", 10, 6)
		writeInstallmentCalcTable(pdf, tr, calcs)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("reports: rendering loan preview pdf: %w", err)
	}
	return buf.Bytes(), nil
}

func writeInstallmentCalcTable(pdf *fpdf.Fpdf, tr func(string) string, calcs []loans.InstallmentCalc) {
	widths := []float64{15, 30, 35, 35, 35}
	writeTableHeaderRow(pdf, tr, widths, []string{"Nº", "Vencimento", "Juros", "Parcela", "Valor a pagar"})
	for _, c := range calcs {
		pdf.CellFormat(widths[0], 5.5, fmt.Sprint(c.Number), "1", 0, "C", false, 0, "")
		pdf.CellFormat(widths[1], 5.5, c.DueDate.Format("02/01/2006"), "1", 0, "C", false, 0, "")
		pdf.CellFormat(widths[2], 5.5, "R$ "+formatBRL(c.Interest), "1", 0, "R", false, 0, "")
		pdf.CellFormat(widths[3], 5.5, "R$ "+formatBRL(c.Principal), "1", 0, "R", false, 0, "")
		pdf.CellFormat(widths[4], 5.5, "R$ "+formatBRL(c.Amount), "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}
}
