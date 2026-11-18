package reports

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/clients"
	"github.com/atlas/atlas-go/internal/loans"
)

// transactionTypeLabel mirrors Java's MovimentacaoPdfLabels.tipoMovimentacaoLabel.
var transactionTypeLabel = map[loans.TransactionType]string{
	loans.TransactionTypeDisbursement:       "Desembolso",
	loans.TransactionTypeInstallmentPayment: "Recebimento (parcela)",
	loans.TransactionTypeOneOffPayment:      "Recebimento (avulso)",
	loans.TransactionTypeForgiveness:        "Perdão",
	loans.TransactionTypeAmortization:       "Amortização",
}

// GenerateTransactionReceiptPDF mirrors MovimentacaoService.gerarReciboPdf (GET
// /api/transactions/{id}/receipt) — a synchronous, single-transaction payment receipt.
func (s *Service) GenerateTransactionReceiptPDF(ctx context.Context, transactionID uuid.UUID) (DownloadedReport, error) {
	txn, err := s.loans.GetTransaction(ctx, transactionID)
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: loading transaction for receipt: %w", err)
	}
	client, err := s.clients.Get(ctx, txn.ClientID)
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: loading client for receipt: %w", err)
	}

	var loanReference string
	if txn.LoanID != nil {
		loan, err := s.loans.Get(ctx, *txn.LoanID)
		if err != nil {
			return DownloadedReport{}, fmt.Errorf("reports: loading loan for receipt: %w", err)
		}
		loanReference = fmt.Sprintf("Contrato de %s — R$ %s", loan.ContractDate.Time().Format("02/01/2006"), formatBRL(loan.PrincipalAmount))
	}

	// Mirrors Java's reciboNumero: "REC-" + first 8 chars of the transaction id, uppercased.
	receiptNumber := "REC-" + strings.ToUpper(strings.ReplaceAll(transactionID.String(), "-", "")[:8])

	companyName, _ := companyBranding(s.settings, ctx)
	pdfBytes, err := buildReceiptPDF(companyName, client, txn, loanReference, receiptNumber)
	if err != nil {
		return DownloadedReport{}, err
	}
	return DownloadedReport{
		Content: pdfBytes, ContentType: "application/pdf",
		Filename: fmt.Sprintf("recibo-%s.pdf", transactionID),
	}, nil
}

func buildReceiptPDF(companyName string, client clients.ClientDTO, txn loans.TransactionDTO, loanReference, receiptNumber string) ([]byte, error) {
	pdf, tr := newReportPDF()

	writeReportHeader(pdf, tr, companyName, "RECIBO")
	pdf.SetFont("Arial", "", 9)
	pdf.SetTextColor(mutedRGB[0], mutedRGB[1], mutedRGB[2])
	pdf.CellFormat(0, 5, tr("Nº "+receiptNumber), "", 1, "C", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.Ln(5)

	pdf.SetFont("Arial", "B", 14)
	pdf.SetFillColor(brandTintRGB[0], brandTintRGB[1], brandTintRGB[2])
	pdf.SetTextColor(brandDarkRGB[0], brandDarkRGB[1], brandDarkRGB[2])
	pdf.SetDrawColor(brandRGB[0], brandRGB[1], brandRGB[2])
	pdf.CellFormat(0, 14, tr("Valor recebido: R$ "+formatBRL(txn.Amount)), "1", 1, "C", true, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.SetDrawColor(0, 0, 0)
	pdf.Ln(5)

	writeSectionTitle(pdf, tr, "Recebido de", 11, 7)
	writeClientCard(pdf, tr, client)

	writeSectionTitle(pdf, tr, "Referente a", 11, 7)
	pdf.SetFont("Arial", "", 9)
	pdf.CellFormat(0, 5, tr(transactionTypeLabel[txn.Type]), "", 1, "L", false, 0, "")
	if loanReference != "" {
		pdf.CellFormat(0, 5, tr(loanReference), "", 1, "L", false, 0, "")
	}
	if txn.Description != nil && *txn.Description != "" {
		pdf.CellFormat(0, 5, tr(*txn.Description), "", 1, "L", false, 0, "")
	}
	pdf.CellFormat(0, 5, tr("Data: "+txn.PaymentDate.Time().Format("02/01/2006")), "", 1, "L", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("reports: rendering receipt pdf: %w", err)
	}
	return buf.Bytes(), nil
}
