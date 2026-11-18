package reports

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/clients"
	"github.com/atlas/atlas-go/internal/jobs"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/atlas/atlas-go/internal/platform/money"
)

// addMoneySafe sums two numeric(15,2)-shaped strings straight from validated DB columns — the
// error is only possible on a malformed input, which can't happen here, so it's discarded rather
// than threaded through every display-only summary call site.
func addMoneySafe(a, b string) string {
	sum, err := money.Add(a, b)
	if err != nil {
		return a
	}
	return sum
}

// processLoanStatement mirrors JobRunnerService.processarExtratoPdf. clientId/kind come from the
// job's payload (set at RequestLoanStatement time).
func (s *Service) processLoanStatement(ctx context.Context, job jobs.Job) (map[string]any, error) {
	clientIDStr, _ := job.Payload["clientId"].(string)
	clientID, err := uuid.Parse(clientIDStr)
	if err != nil {
		return nil, fmt.Errorf("reports: statement job has an invalid clientId: %w", err)
	}
	kind := normalizeStatementKind(fmt.Sprint(job.Payload["kind"]))

	client, err := s.clients.Get(ctx, clientID)
	if err != nil {
		return nil, fmt.Errorf("reports: loading client for statement: %w", err)
	}

	clientLoans, err := s.loans.List(ctx, &clientID, "")
	if err != nil {
		return nil, fmt.Errorf("reports: listing client loans: %w", err)
	}
	sort.Slice(clientLoans, func(i, j int) bool {
		return clientLoans[i].ContractDate.Time().After(clientLoans[j].ContractDate.Time())
	})

	transactions, err := s.loans.ListAllTransactions(ctx, &clientID)
	if err != nil {
		return nil, fmt.Errorf("reports: listing client transactions: %w", err)
	}
	sort.Slice(transactions, func(i, j int) bool {
		return transactions[i].PaymentDate.Time().Before(transactions[j].PaymentDate.Time())
	})
	txByLoan := make(map[uuid.UUID][]loans.TransactionDTO)
	for _, t := range transactions {
		if t.LoanID != nil {
			txByLoan[*t.LoanID] = append(txByLoan[*t.LoanID], t)
		}
	}

	// One ListInstallments round trip per loan — bounded by this client's own loan count (typically
	// a handful), not the whole portfolio, so the batch N+1 concern flagged for an all-clients pass
	// elsewhere doesn't apply at the same scale here.
	installmentsByLoan := make(map[uuid.UUID][]loans.InstallmentDTO, len(clientLoans))
	for _, loan := range clientLoans {
		insts, err := s.loans.ListInstallments(ctx, loan.ID)
		if err != nil {
			return nil, fmt.Errorf("reports: listing installments for loan %s: %w", loan.ID, err)
		}
		installmentsByLoan[loan.ID] = insts
	}

	now := civildate.Today().Time()
	currentMonth := loans.YearMonth{Year: now.Year(), Month: int(now.Month())}
	nextMonth := currentMonth.AddMonths(1)
	vigentes, err := s.loans.Vigentes(ctx, []loans.YearMonth{currentMonth, nextMonth})
	if err != nil {
		return nil, fmt.Errorf("reports: computing vigentes for statement: %w", err)
	}

	companyName, _ := companyBranding(s.settings, ctx)

	pdf, err := buildLoanStatementPDF(statementData{
		companyName: companyName, client: client, kind: kind,
		loans: clientLoans, transactionsByLoan: txByLoan, installmentsByLoan: installmentsByLoan,
		currentMonth: currentMonth, nextMonth: nextMonth,
		currentMonthVigentes: filterByClient(vigentes[currentMonth], clientID),
		nextMonthVigentes:    filterByClient(vigentes[nextMonth], clientID),
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

func filterByClient(list []loans.LoanSummaryDTO, clientID uuid.UUID) []loans.LoanSummaryDTO {
	var out []loans.LoanSummaryDTO
	for _, l := range list {
		if l.ClientID == clientID {
			out = append(out, l)
		}
	}
	return out
}

type statementData struct {
	companyName          string
	client               clients.ClientDTO
	kind                 string
	loans                []loans.LoanDTO
	transactionsByLoan   map[uuid.UUID][]loans.TransactionDTO
	installmentsByLoan   map[uuid.UUID][]loans.InstallmentDTO
	currentMonth         loans.YearMonth
	nextMonth            loans.YearMonth
	currentMonthVigentes []loans.LoanSummaryDTO
	nextMonthVigentes    []loans.LoanSummaryDTO
}

// pageWidth is the usable content width at 15mm margins on A4 (210mm - 2*15mm).
const pageWidth = 180.0

func buildLoanStatementPDF(d statementData) ([]byte, error) {
	pdf, tr := newReportPDF()

	title := "EXTRATO DE CLIENTE"
	switch d.kind {
	case StatementKindOutstanding:
		title = "EXTRATO DE PENDÊNCIAS"
	case StatementKindLoansDue:
		title = "EMPRÉSTIMOS DEVIDOS"
	}
	writeReportHeader(pdf, tr, d.companyName, title)

	pdf.Ln(4)
	writeClientCard(pdf, tr, d.client)

	switch d.kind {
	case StatementKindLoansDue:
		writeLoansDueTable(pdf, tr, d)
	case StatementKindOutstanding:
		writeOutstandingSection(pdf, tr, "Pendências — "+monthLabel(d.currentMonth.Year, d.currentMonth.Month), d.currentMonthVigentes, true)
		pdf.Ln(4)
		writeOutstandingSection(pdf, tr, "Pagamentos futuros — "+monthLabel(d.nextMonth.Year, d.nextMonth.Month), d.nextMonthVigentes, false)
	default:
		writeSummary(pdf, tr, d.loans, d.transactionsByLoan)
		for i, loan := range d.loans {
			pdf.AddPage()
			writeLoanSection(pdf, tr, i+1, loan, d.installmentsByLoan[loan.ID], d.transactionsByLoan[loan.ID])
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("reports: rendering statement pdf: %w", err)
	}
	return buf.Bytes(), nil
}

// writeReportHeader draws a full-bleed brand-colored band across the top of the page (letterhead,
// not just a heading) — title and company name in white inside the band, the generated-at
// timestamp in muted gray just below it. Rect uses absolute page coordinates regardless of the
// text cursor, so this works whether or not AddPage() just ran; the explicit SetXY afterward is
// what re-anchors the cursor to the left margin for CellFormat's centering to line up with the
// rest of the page's 180mm content width (pageWidth) instead of the full 210mm page width.
func writeReportHeader(pdf *fpdf.Fpdf, tr func(string) string, companyName, title string) {
	const bandHeight = 24.0
	pdf.SetFillColor(brandRGB[0], brandRGB[1], brandRGB[2])
	pdf.Rect(0, 0, 210, bandHeight, "F")

	pdf.SetXY(15, 6)
	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont("Arial", "B", 16)
	pdf.CellFormat(0, 9, tr(title), "", 1, "C", false, 0, "")
	if companyName != "" {
		pdf.SetX(15)
		pdf.SetFont("Arial", "", 10)
		pdf.CellFormat(0, 6, tr(companyName), "", 1, "C", false, 0, "")
	}

	pdf.SetXY(15, bandHeight+4)
	pdf.SetFont("Arial", "", 8)
	pdf.SetTextColor(mutedRGB[0], mutedRGB[1], mutedRGB[2])
	pdf.CellFormat(0, 5, tr("Gerado em "+time.Now().Format("02/01/2006 15:04")), "", 1, "C", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
}

// writeClientCard draws the client's name/contact block as a pale brand-tinted card with a solid
// brand-colored accent bar on the left edge, instead of plain unstyled lines — the height is
// computed from which optional fields are actually present so the tint box hugs the content
// instead of leaving (or cutting off) space for absent fields.
func writeClientCard(pdf *fpdf.Fpdf, tr func(string) string, c clients.ClientDTO) {
	lines := 0
	for _, present := range []bool{c.Phone != nil, c.Email != nil, c.CPFCNPJ != nil, c.PixKey != nil} {
		if present {
			lines++
		}
	}
	boxHeight := 11.0 + float64(lines)*5.0

	x, y := pdf.GetX(), pdf.GetY()
	pdf.SetFillColor(brandTintRGB[0], brandTintRGB[1], brandTintRGB[2])
	pdf.Rect(x, y, pageWidth, boxHeight, "F")
	pdf.SetFillColor(brandRGB[0], brandRGB[1], brandRGB[2])
	pdf.Rect(x, y, 2.5, boxHeight, "F")

	pdf.SetXY(x+6, y+3)
	pdf.SetFont("Arial", "B", 12)
	pdf.SetTextColor(brandDarkRGB[0], brandDarkRGB[1], brandDarkRGB[2])
	pdf.CellFormat(0, 6, tr(c.Name), "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "", 9)
	pdf.SetTextColor(70, 70, 70)
	if c.Phone != nil {
		pdf.SetX(x + 6)
		pdf.CellFormat(0, 5, tr("Telefone: "+*c.Phone), "", 1, "L", false, 0, "")
	}
	if c.Email != nil {
		pdf.SetX(x + 6)
		pdf.CellFormat(0, 5, tr("E-mail: "+*c.Email), "", 1, "L", false, 0, "")
	}
	if c.CPFCNPJ != nil {
		pdf.SetX(x + 6)
		pdf.CellFormat(0, 5, tr("CPF/CNPJ: "+*c.CPFCNPJ), "", 1, "L", false, 0, "")
	}
	if c.PixKey != nil {
		pdf.SetX(x + 6)
		pdf.CellFormat(0, 5, tr("Chave PIX: "+*c.PixKey), "", 1, "L", false, 0, "")
	}

	pdf.SetTextColor(0, 0, 0)
	pdf.SetXY(x, y+boxHeight+4)
}

func writeSummary(pdf *fpdf.Fpdf, tr func(string) string, loanList []loans.LoanDTO, txByLoan map[uuid.UUID][]loans.TransactionDTO) {
	var active, paidOff int
	totalLent, outstanding := "0.00", "0.00"
	for _, l := range loanList {
		switch l.Status {
		case loans.StatusActive, loans.StatusOverdue:
			active++
			outstanding = addMoneySafe(outstanding, l.OutstandingBalance)
		case loans.StatusPaidOff:
			paidOff++
		}
		totalLent = addMoneySafe(totalLent, l.PrincipalAmount)
	}

	received, paidOut := "0.00", "0.00"
	for _, txs := range txByLoan {
		for _, t := range txs {
			switch t.Type {
			case loans.TransactionTypeDisbursement:
				paidOut = addMoneySafe(paidOut, t.Amount)
			case loans.TransactionTypeForgiveness:
				// neither bucket — not real cash flow
			default:
				received = addMoneySafe(received, t.Amount)
			}
		}
	}

	writeSectionTitle(pdf, tr, "Resumo geral", 11, 7)
	pdf.SetFont("Arial", "", 9)
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Empréstimos: %d (%d ativos, %d quitados)", len(loanList), active, paidOff)), "", 1, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Valor total emprestado: R$ %s", formatBRL(totalLent))), "", 1, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Saldo devedor total: R$ %s", formatBRL(outstanding))), "", 1, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Recebido: R$ %s | Desembolsado: R$ %s", formatBRL(received), formatBRL(paidOut))), "", 1, "L", false, 0, "")
	pdf.Ln(3)
}

var paymentTypeLabel = map[loans.PaymentType]string{
	loans.PaymentTypeInstallment:       "Parcelado",
	loans.PaymentTypeRecurringInterest: "Somente juros",
}

// loanStatusLabel/installmentStatusLabel/monthStatusLabel mirror the frontend's own
// STATUS_FROM_GO/PARCELA_STATUS_FROM_GO translation tables (lib/api/emprestimos.ts) — the PDF must
// show the same Portuguese terms the web UI already does, not the bare English DB enum values.
var loanStatusLabel = map[loans.Status]string{
	loans.StatusActive:   "Ativo",
	loans.StatusOverdue:  "Inadimplente",
	loans.StatusPaidOff:  "Quitado",
	loans.StatusCanceled: "Cancelado",
}

var installmentStatusLabel = map[loans.InstallmentStatus]string{
	loans.InstallmentStatusPending:   "Pendente",
	loans.InstallmentStatusPaid:      "Paga",
	loans.InstallmentStatusOverdue:   "Atrasada",
	loans.InstallmentStatusCancelled: "Cancelada",
}

// outstandingStatusLabel is writeOutstandingSection's own Title-Case rendering of
// loans.MonthStatus, distinct from spreadsheet.go's monthStatusLabel (which needs the
// emoji-prefixed, all-caps convention that fits an Excel cell, not a PDF table row).
var outstandingStatusLabel = map[loans.MonthStatus]string{
	loans.MonthStatusPaid:    "Pago",
	loans.MonthStatusPending: "Pendente",
	loans.MonthStatusOverdue: "Atrasado",
	loans.MonthStatusPaidOff: "Quitado",
}

var installmentStatusColor = map[loans.InstallmentStatus][3]int{
	loans.InstallmentStatusPaid:    {200, 230, 200},
	loans.InstallmentStatusOverdue: {250, 210, 210},
}

func writeLoanSection(pdf *fpdf.Fpdf, tr func(string) string, index int, loan loans.LoanDTO, installments []loans.InstallmentDTO, txs []loans.TransactionDTO) {
	writeSectionTitle(pdf, tr, fmt.Sprintf("Empréstimo #%d", index), 13, 8)
	pdf.SetFont("Arial", "", 9)
	pdf.CellFormat(pageWidth/2, 5, tr("Contratado em: "+loan.ContractDate.Time().Format("02/01/2006")), "", 0, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr("Status: "+loanStatusLabel[loan.Status]), "", 1, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Principal: R$ %s", formatBRL(loan.PrincipalAmount))), "", 0, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Taxa mensal: %s%%", formatPercentBR(loan.MonthlyInterestRate))), "", 1, "L", false, 0, "")
	pdf.CellFormat(pageWidth/2, 5, tr("Tipo: "+paymentTypeLabel[loan.PaymentType]), "", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "B", 9)
	pdf.CellFormat(pageWidth/2, 5, tr(fmt.Sprintf("Saldo devedor: R$ %s", formatBRL(loan.OutstandingBalance))), "", 1, "L", false, 0, "")
	pdf.Ln(2)

	if len(installments) > 0 {
		widths := []float64{15, 30, 35, 40, 40, 20}
		headers := []string{"Nº", "Vencimento", "Valor", "Saldo devedor", "Status"}
		writeTableHeaderRow(pdf, tr, widths[:5], headers)
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
		pdf.Ln(3)
	}

	if len(txs) > 0 {
		writeSectionTitle(pdf, tr, "Movimentações", 9, 6)
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
}

func writeOutstandingSection(pdf *fpdf.Fpdf, tr func(string) string, heading string, rows []loans.LoanSummaryDTO, isCurrentMonth bool) {
	writeSectionTitle(pdf, tr, heading, 11, 7)
	if len(rows) == 0 {
		pdf.SetFont("Arial", "", 9)
		pdf.CellFormat(0, 6, tr("Nenhum empréstimo neste período."), "", 1, "L", false, 0, "")
		return
	}

	widths := []float64{44, 26, 18, 30, 28, 34}
	writeTableHeaderRow(pdf, tr, widths, []string{"Cliente/Empréstimo", "Vencimento", "Taxa", "Valor", "Status", "Saldo devedor"})
	totalValor, totalOutstanding := "0.00", "0.00"
	for _, r := range rows {
		due := ""
		if r.InstallmentDueDate != nil {
			due = r.InstallmentDueDate.Time().Format("02/01/2006")
		}
		status := outstandingStatusLabel[r.Status]
		if !isCurrentMonth {
			status = "Agendado"
		}
		totalValor = addMoneySafe(totalValor, r.InterestThisMonth)
		totalOutstanding = addMoneySafe(totalOutstanding, r.OutstandingBalance)
		pdf.CellFormat(widths[0], 5.5, tr(r.ClientName), "1", 0, "L", false, 0, "")
		pdf.CellFormat(widths[1], 5.5, due, "1", 0, "C", false, 0, "")
		pdf.CellFormat(widths[2], 5.5, effectiveMonthlyRateBR(r.InterestThisMonth, r.OutstandingBalance), "1", 0, "C", false, 0, "")
		pdf.CellFormat(widths[3], 5.5, "R$ "+formatBRL(r.InterestThisMonth), "1", 0, "R", false, 0, "")
		pdf.CellFormat(widths[4], 5.5, tr(status), "1", 0, "C", false, 0, "")
		pdf.CellFormat(widths[5], 5.5, "R$ "+formatBRL(r.OutstandingBalance), "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}

	pdf.SetFont("Arial", "B", 8)
	pdf.CellFormat(widths[0]+widths[1]+widths[2], 6, tr("TOTAL"), "1", 0, "R", false, 0, "")
	pdf.CellFormat(widths[3], 6, "R$ "+formatBRL(totalValor), "1", 0, "R", false, 0, "")
	pdf.CellFormat(widths[4], 6, "", "1", 0, "C", false, 0, "")
	pdf.CellFormat(widths[5], 6, "R$ "+formatBRL(totalOutstanding), "1", 0, "R", false, 0, "")
	pdf.Ln(-1)
	pdf.SetFont("Arial", "", 8)
}

// nextOpenInstallment returns the earliest still-open (PENDING/OVERDUE) installment by due date,
// or nil if the loan has none — the row's "próxima parcela" and its interest portion come from it.
func nextOpenInstallment(installments []loans.InstallmentDTO) *loans.InstallmentDTO {
	var next *loans.InstallmentDTO
	for i := range installments {
		inst := installments[i]
		if inst.Status != loans.InstallmentStatusPending && inst.Status != loans.InstallmentStatusOverdue {
			continue
		}
		if next == nil || inst.DueDate.Time().Before(next.DueDate.Time()) {
			next = &installments[i]
		}
	}
	return next
}

// writeLoansDueTable renders the LOANS_DUE statement: one consolidated table row per loan that
// still owes money (ACTIVE/OVERDUE), numbered #1.. by contract date ascending, plus a bold total
// row. "Juros no mês" is the interest portion of the loan's next open installment (for a
// recurring-interest loan that's balance*rate; for an installment loan it's that installment's
// frozen interest), falling back to the current-month vigentes figure when there's no open
// installment.
func writeLoansDueTable(pdf *fpdf.Fpdf, tr func(string) string, d statementData) {
	due := make([]loans.LoanDTO, 0, len(d.loans))
	for _, l := range d.loans {
		if l.Status == loans.StatusActive || l.Status == loans.StatusOverdue {
			due = append(due, l)
		}
	}
	sort.SliceStable(due, func(i, j int) bool {
		return due[i].ContractDate.Time().Before(due[j].ContractDate.Time())
	})

	writeSectionTitle(pdf, tr, "Empréstimos com saldo devedor", 11, 7)
	if len(due) == 0 {
		pdf.SetFont("Arial", "", 9)
		pdf.CellFormat(0, 6, tr("Este cliente não tem empréstimos com saldo devedor."), "", 1, "L", false, 0, "")
		return
	}

	interestByLoan := make(map[uuid.UUID]string, len(d.currentMonthVigentes))
	for _, v := range d.currentMonthVigentes {
		interestByLoan[v.LoanID] = v.InterestThisMonth
	}

	widths := []float64{10, 26, 30, 18, 30, 66}
	writeTableHeaderRow(pdf, tr, widths, []string{"#", "Contratado", "Valor devido", "Taxa", "Juros no mês", "Próxima parcela"})

	totalOutstanding, totalInterest := "0.00", "0.00"
	for i, loan := range due {
		next := nextOpenInstallment(d.installmentsByLoan[loan.ID])

		interest := "0.00"
		nextParcela := "—"
		if next != nil {
			interest = next.InterestAmount
			nextParcela = next.DueDate.Time().Format("02/01/2006") + " — R$ " + formatBRL(next.InstallmentAmount)
		} else if v, ok := interestByLoan[loan.ID]; ok {
			interest = v
		}

		totalOutstanding = addMoneySafe(totalOutstanding, loan.OutstandingBalance)
		totalInterest = addMoneySafe(totalInterest, interest)

		pdf.CellFormat(widths[0], 5.5, fmt.Sprint(i+1), "1", 0, "C", false, 0, "")
		pdf.CellFormat(widths[1], 5.5, loan.ContractDate.Time().Format("02/01/2006"), "1", 0, "C", false, 0, "")
		pdf.CellFormat(widths[2], 5.5, "R$ "+formatBRL(loan.OutstandingBalance), "1", 0, "R", false, 0, "")
		pdf.CellFormat(widths[3], 5.5, formatPercentBR(loan.MonthlyInterestRate)+"%", "1", 0, "C", false, 0, "")
		pdf.CellFormat(widths[4], 5.5, "R$ "+formatBRL(interest), "1", 0, "R", false, 0, "")
		pdf.CellFormat(widths[5], 5.5, tr(nextParcela), "1", 0, "L", false, 0, "")
		pdf.Ln(-1)
	}

	pdf.SetFont("Arial", "B", 8)
	pdf.CellFormat(widths[0]+widths[1], 6, tr("TOTAL"), "1", 0, "R", false, 0, "")
	pdf.CellFormat(widths[2], 6, "R$ "+formatBRL(totalOutstanding), "1", 0, "R", false, 0, "")
	pdf.CellFormat(widths[3], 6, "", "1", 0, "C", false, 0, "")
	pdf.CellFormat(widths[4], 6, "R$ "+formatBRL(totalInterest), "1", 0, "R", false, 0, "")
	pdf.CellFormat(widths[5], 6, "", "1", 0, "L", false, 0, "")
	pdf.Ln(-1)

	pdf.SetFont("Arial", "", 8)
	pdf.SetTextColor(mutedRGB[0], mutedRGB[1], mutedRGB[2])
	pdf.Ln(2)
	pdf.CellFormat(0, 5, tr(fmt.Sprintf("%d empréstimo(s) com saldo devedor.", len(due))), "", 1, "L", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
}
