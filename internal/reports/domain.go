// Package reports generates the same three report types as the old Java relatorios module — a
// client loan statement PDF, a legacy spreadsheet export, and a combined expense+income financial
// PDF — as durable background jobs (internal/jobs), owned by Claude (D-005). Ported from
// RelatorioService/RelatorioFinanceiroService/PlanilhaExportService/JobRunnerService/JobController,
// read from the real Java source rather than guessed.
//
// Deliberately different from Java: PDF/xlsx bytes are built directly in Go (fpdf/excelize)
// instead of Thymeleaf+OpenHTMLtoPDF — no HTML template, no extra system dependency (wkhtmltopdf/
// Chromium) needed in the deployment image. The computed data and business rules mirror Java
// exactly; the visual layout is a clean, simpler equivalent, not a pixel-for-pixel port of the CSS.
package reports

// Type mirrors JobTipo, translated to English per the naming convention.
const (
	TypeLoanStatement     = "LOAN_STATEMENT"     // EXTRATO_PDF
	TypeSpreadsheetExport = "SPREADSHEET_EXPORT" // EXPORT_PLANILHA
	TypeFinancialReport   = "FINANCIAL_REPORT"   // RELATORIO_FINANCEIRO
)

// StatementKind mirrors the "tipo" query param on the old /clientes/{id}/extrato endpoint.
const (
	StatementKindComplete    = "COMPLETE"    // COMPLETO — full statement, one section per loan
	StatementKindOutstanding = "OUTSTANDING" // PENDENCIAS — this month's dues + next month's preview only
	StatementKindLoansDue    = "LOANS_DUE"   // EMPRÉSTIMOS DEVIDOS — one consolidated table row per still-owed loan
)

// normalizeStatementKind mirrors RelatorioService/JobRunnerService's defensive re-normalization:
// anything other than a recognized kind is treated as COMPLETE.
func normalizeStatementKind(kind string) string {
	switch kind {
	case StatementKindOutstanding, StatementKindLoansDue:
		return kind
	default:
		return StatementKindComplete
	}
}
