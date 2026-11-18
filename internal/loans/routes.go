package loans

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition
// (postgres.NewRepository -> NewService -> NewHandler) happens in cmd/api/main.go — see
// internal/categories/routes.go for why (import-cycle avoidance; cmd/api/main.go is Hermes-owned
// per D-005).
//
// Every handler is gated by httpx.RequirePerm (D-003) — retrofitted after
// internal/platform/httpx/authz.go landed; see internal/expenses/routes.go for the reference
// pattern this follows.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "loans"
	mux.HandleFunc("GET /api/loans", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/loans", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	// The literal "vigentes" segment takes precedence over the {id} wildcard at the same position
	// (Go 1.22 ServeMux: more specific patterns win) — must still be registered, order doesn't matter.
	mux.HandleFunc("GET /api/loans/vigentes", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Vigentes)))
	mux.HandleFunc("GET /api/loans/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.Get)))
	mux.HandleFunc("PUT /api/loans/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Update)))
	mux.HandleFunc("DELETE /api/loans/{id}", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Delete)))
	mux.HandleFunc("POST /api/loans/{id}/cancel", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Cancel)))
	mux.HandleFunc("PATCH /api/loans/{id}/due-day", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.ChangeDueDay)))
	mux.HandleFunc("PATCH /api/loans/{id}/interest-rate", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.ChangeInterestRate)))
	mux.HandleFunc("GET /api/loans/{id}/installments", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListInstallments)))
	mux.HandleFunc("POST /api/loans/{loanId}/installments/{installmentId}/pay", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.MarkInstallmentPaid)))
	mux.HandleFunc("PATCH /api/loans/{loanId}/installments/{installmentId}/postpone", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.PostponeInstallment)))
	mux.HandleFunc("POST /api/loans/{id}/amortize", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.ApplyAmortization)))
	mux.HandleFunc("GET /api/loans/{id}/transactions", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListTransactions)))
	mux.HandleFunc("POST /api/loans/{id}/transactions", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.RecordTransaction)))
	// Flat, cross-loan ledger — one query instead of the frontend fanning out a request per loan
	// (see Repository.ListAllTransactions's doc comment).
	mux.HandleFunc("GET /api/transactions", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.ListAllTransactions)))
}
