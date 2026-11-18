package reports

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go.
// Gated by httpx.RequirePerm (D-003), module "reports" — a report is generated from data across
// several other modules (loans/expenses/income), so it gets its own permission rather than piggy-
// backing on one of theirs.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "reports"
	mux.HandleFunc("POST /api/reports/clients/{id}/statement", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.RequestLoanStatement)))
	mux.HandleFunc("POST /api/reports/spreadsheet", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.RequestSpreadsheetExport)))
	mux.HandleFunc("POST /api/reports/financial", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.RequestFinancialReport)))
	mux.HandleFunc("GET /api/jobs/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.GetJob)))
	mux.HandleFunc("GET /api/jobs/{id}/download", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.DownloadJob)))

	// Gated on their own module (not "reports"), same reasoning as the loan routes below — a
	// quick per-module PDF export available to anyone who can already read that module's data,
	// not conditioned on separate reports access. Mirrors the old GastoRelatorioController/
	// ReceitaRelatorioController, which lived under gastos/receitas permissions, not a shared one.
	mux.HandleFunc("GET /api/expenses/report", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, "expenses", httpx.PermRead, handler.GenerateExpenseReport)))
	mux.HandleFunc("GET /api/income/report", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, "income", httpx.PermRead, handler.GenerateIncomeReport)))

	// Gated on "loans" (not "reports"): mirrors EmprestimoController's /api/emprestimos/{id}/extrato,
	// which lives under the loans permission, not a separate reports one — a loan-detail action
	// available to anyone who can read that loan, not conditioned on reports access.
	mux.HandleFunc("GET /api/loans/{id}/statement", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, "loans", httpx.PermRead, handler.GenerateLoanStatement)))
	// Preview takes the same request body as POST /api/loans and persists nothing — gated the same
	// way as loan creation (PermWrite on "loans"), mirroring EmprestimoController.simulacaoPdf.
	mux.HandleFunc("POST /api/loans/preview/pdf", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, "loans", httpx.PermWrite, handler.GenerateLoanPreview)))
	// Gated on "loans" (not "reports"), same reasoning as the statement route above — mirrors
	// MovimentacaoController.recibo.
	mux.HandleFunc("GET /api/transactions/{id}/receipt", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, "loans", httpx.PermRead, handler.GenerateTransactionReceipt)))

	// Gated on "health" (not "reports"), same reasoning as expenses/income reports above — the
	// emergency card is built from health records, so it's available to anyone who can already
	// read that person's health data.
	mux.HandleFunc("GET /api/people/{id}/emergency-card", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, "health", httpx.PermRead, handler.GenerateEmergencyCard)))
}
