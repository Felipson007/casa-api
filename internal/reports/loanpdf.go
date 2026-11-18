package reports

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/clients"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/atlas/atlas-go/internal/platform/money"
)

// GenerateLoanStatementPDF mirrors EmprestimoExtratoPdfService.gerarExtratoPdf — a SYNCHRONOUS
// single-loan statement (GET /api/loans/{id}/statement, called directly via window.location.href
// from the loan detail page's "Baixar extrato (PDF)" button, not through the async job queue the
// rest of this package uses: Java builds this one fast enough that a job would be pure overhead).
func (s *Service) GenerateLoanStatementPDF(ctx context.Context, loanID uuid.UUID) (DownloadedReport, error) {
	loan, err := s.loans.Get(ctx, loanID)
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: loading loan for statement: %w", err)
	}
	client, err := s.clients.Get(ctx, loan.ClientID)
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: loading client for statement: %w", err)
	}

	allInstallments, err := s.loans.ListInstallments(ctx, loanID)
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: listing installments for statement: %w", err)
	}
	sort.Slice(allInstallments, func(i, j int) bool {
		return allInstallments[i].InstallmentNumber < allInstallments[j].InstallmentNumber
	})
	installments := installmentsForStatement(loan, allInstallments)

	txs, err := s.loans.ListTransactions(ctx, loanID)
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: listing transactions for statement: %w", err)
	}
	sort.Slice(txs, func(i, j int) bool { return txs[i].PaymentDate.Time().Before(txs[j].PaymentDate.Time()) })

	totalPago := money.Zero
	for _, t := range txs {
		if t.Type != loans.TransactionTypeDisbursement {
			totalPago = addMoneySafe(totalPago, t.Amount)
		}
	}
	percentualQuitado := 0.0
	if principal, err := money.Parse(loan.PrincipalAmount); err == nil && principal.Sign() > 0 {
		if quitadoStr, err := money.Sub(loan.PrincipalAmount, loan.OutstandingBalance); err == nil {
			if quitado, err := money.Parse(quitadoStr); err == nil {
				ratio := new(big.Rat).Quo(quitado, principal)
				f, _ := ratio.Float64()
				percentualQuitado = f * 100
			}
		}
	}

	companyName, _ := companyBranding(s.settings, ctx)
	pdfBytes, err := buildSingleLoanStatementPDF(companyName, client, loan, installments, txs, totalPago, percentualQuitado)
	if err != nil {
		return DownloadedReport{}, err
	}

	return DownloadedReport{
		Content: pdfBytes, ContentType: "application/pdf",
		Filename: fmt.Sprintf("extrato-emprestimo-%s.pdf", loanID),
	}, nil
}

// installmentsForStatement mirrors EmprestimoExtratoPdfService.parcelasParaExtrato: a
// RECURRING_INTEREST loan's schedule is generated a year ahead (mostly not-yet-due projections),
// so showing all of it is noise — only the next pending installment, or (if already overdue) the
// full arrears. An INSTALLMENT loan always shows its whole contracted schedule.
func installmentsForStatement(loan loans.LoanDTO, all []loans.InstallmentDTO) []loans.InstallmentDTO {
	if loan.PaymentType == loans.PaymentTypeInstallment {
		return all
	}
	var pending []loans.InstallmentDTO
	for _, i := range all {
		if i.Status != loans.InstallmentStatusPaid {
			pending = append(pending, i)
		}
	}
	if len(pending) == 0 {
		return pending
	}
	next := pending[0]
	today := civildate.Today().Time()
	overdue := next.Status == loans.InstallmentStatusOverdue || next.DueDate.Time().Before(today)
	if overdue {
		return pending
	}
	return []loans.InstallmentDTO{next}
}

func buildSingleLoanStatementPDF(
	companyName string, client clients.ClientDTO, loan loans.LoanDTO, installments []loans.InstallmentDTO,
	txs []loans.TransactionDTO, totalPago string, percentualQuitado float64,
) ([]byte, error) {
	pdf, tr := newReportPDF()

	writeReportHeader(pdf, tr, companyName, "EXTRATO DE EMPRÉSTIMO")
	pdf.Ln(4)
	writeClientCard(pdf, tr, client)

	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(pageWidth/2, 6, tr("Contratado em: "+loan.ContractDate.Time().Format("02/01/2006")), "", 0, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 6, tr("Status: "+loanStatusLabel[loan.Status]), "", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "", 9)
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Principal: R$ %s", formatBRL(loan.PrincipalAmount))), "", 0, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Taxa mensal: %s%%", formatPercentBR(loan.MonthlyInterestRate))), "", 1, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr("Tipo: "+paymentTypeLabel[loan.PaymentType]), "", 0, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Quitado: %s%%", formatFloatBR(percentualQuitado, 1))), "", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(pageWidth/2, 6, tr(fmt.Sprintf("Saldo devedor: R$ %s", formatBRL(loan.OutstandingBalance))), "", 0, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 6, tr(fmt.Sprintf("Total pago: R$ %s", formatBRL(totalPago))), "", 1, "L", false, 0, "")
	pdf.Ln(3)

	if len(installments) > 0 {
		writeSectionTitle(pdf, tr, "Parcelas", 10, 6)
		widths := []float64{15, 30, 35, 40, 40}
		writeTableHeaderRow(pdf, tr, widths, []string{"Nº", "Vencimento", "Valor", "Saldo devedor", "Status"})
		for _, inst := range installments {
			color := installmentStatusColor[inst.Status]
			fill := color != [3]int{}
			if fill {
				pdf.SetFillColor(color[0], color[1], color[2])
			}
			pdf.CellFormat(widths[0], 5.5, fmt.Sprint(inst.InstallmentNumber), "1", 0, "C", fill, 0, "")
			pdf.CellFormat(widths[1], 5.5, inst.DueDate.Time().Format("02/01/2006"), "1", 0, "C", fill, 0, "")
			pdf.CellFormat(widths[2], 5.5, "R$ "+formatBRL(inst.InstallmentAmount), "1", 0, "R", fill, 0, "")
			pdf.CellFormat(widths[3], 5.5, "R$ "+formatBRL(inst.OutstandingBalance), "1", 0, "R", fill, 0, "")
			pdf.CellFormat(widths[4], 5.5, tr(installmentStatusLabel[inst.Status]), "1", 0, "C", fill, 0, "")
			pdf.Ln(-1)
		}
		pdf.Ln(4)
	}

	if len(txs) > 0 {
		writeSectionTitle(pdf, tr, "Movimentações", 10, 6)
		widths := []float64{30, 45, 35, 70}
		writeTableHeaderRow(pdf, tr, widths, []string{"Data", "Tipo", "Valor", "Descrição"})
		for _, t := range txs {
			desc := ""
			if t.Description != nil {
				desc = *t.Description
			}
			pdf.CellFormat(widths[0], 5.5, t.PaymentDate.Time().Format("02/01/2006"), "1", 0, "C", false, 0, "")
			pdf.CellFormat(widths[1], 5.5, tr(transactionTypeLabel[t.Type]), "1", 0, "C", false, 0, "")
			pdf.CellFormat(widths[2], 5.5, "R$ "+formatBRL(t.Amount), "1", 0, "R", false, 0, "")
			pdf.CellFormat(widths[3], 5.5, tr(desc), "1", 0, "L", false, 0, "")
			pdf.Ln(-1)
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("reports: rendering loan statement pdf: %w", err)
	}
	return buf.Bytes(), nil
}
