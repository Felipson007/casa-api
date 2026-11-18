package reports

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP adapter for both report-generation (RelatorioController) and job status/
// download (JobController) — combined here since reports.Service owns both.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RequestLoanStatement handles POST /api/reports/clients/{id}/statement — mirrors
// RelatorioController.solicitarExtrato. ?type= defaults to COMPLETE (COMPLETO).
func (h *Handler) RequestLoanStatement(w http.ResponseWriter, r *http.Request) error {
	clientID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	job, err := h.service.RequestLoanStatement(r.Context(), clientID, r.URL.Query().Get("type"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusAccepted, job)
	return nil
}

// RequestSpreadsheetExport handles POST /api/reports/spreadsheet.
func (h *Handler) RequestSpreadsheetExport(w http.ResponseWriter, r *http.Request) error {
	job, err := h.service.RequestSpreadsheetExport(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusAccepted, job)
	return nil
}

// RequestFinancialReport handles POST /api/reports/financial?yearStart=&yearEnd=.
func (h *Handler) RequestFinancialReport(w http.ResponseWriter, r *http.Request) error {
	yearStart, err := parseRequiredYear(r, "yearStart")
	if err != nil {
		return err
	}
	yearEnd := yearStart
	if raw := r.URL.Query().Get("yearEnd"); raw != "" {
		yearEnd, err = strconv.Atoi(raw)
		if err != nil {
			return apperr.Validation("yearEnd must be an integer", "yearEnd")
		}
	}
	job, err := h.service.RequestFinancialReport(r.Context(), yearStart, yearEnd)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusAccepted, job)
	return nil
}

func parseRequiredYear(r *http.Request, param string) (int, error) {
	raw := r.URL.Query().Get(param)
	if raw == "" {
		return 0, apperr.Validation(param+" is required", param)
	}
	year, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apperr.Validation(param+" must be an integer", param)
	}
	return year, nil
}

// GenerateLoanStatement handles GET /api/loans/{id}/statement — mirrors
// EmprestimoController.extrato: a SYNCHRONOUS single-loan PDF (unlike RequestLoanStatement's async
// per-client job), served directly via window.location.href from the loan detail page.
func (h *Handler) GenerateLoanStatement(w http.ResponseWriter, r *http.Request) error {
	loanID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	file, err := h.service.GenerateLoanStatementPDF(r.Context(), loanID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, file.Filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Content)
	return nil
}

// GenerateLoanPreview handles POST /api/loans/preview/pdf — mirrors
// EmprestimoController.simulacaoPdf: a synchronous, non-persisting PDF preview of a loan's
// schedule, built from the same request body as POST /api/loans.
func (h *Handler) GenerateLoanPreview(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[loans.CreateLoanRequest](r)
	if err != nil {
		return err
	}
	file, err := h.service.GenerateLoanPreviewPDF(r.Context(), req)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, file.Filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Content)
	return nil
}

// GenerateTransactionReceipt handles GET /api/transactions/{id}/receipt — mirrors
// MovimentacaoController.recibo.
func (h *Handler) GenerateTransactionReceipt(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	file, err := h.service.GenerateTransactionReceiptPDF(r.Context(), id)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, file.Filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Content)
	return nil
}

// GenerateEmergencyCard handles GET /api/people/{id}/emergency-card — a synchronous,
// single-person PDF replacing the on-screen /emergencia page's old window.print() flow.
func (h *Handler) GenerateEmergencyCard(w http.ResponseWriter, r *http.Request) error {
	personID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	file, err := h.service.GenerateEmergencyCardPDF(r.Context(), personID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, file.Filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Content)
	return nil
}

// GenerateExpenseReport handles GET /api/expenses/report?year=&month= — mirrors the old
// GastoRelatorioController.relatorio: a synchronous PDF of every expense in the given year,
// optionally narrowed to one month. month is optional; year is required.
func (h *Handler) GenerateExpenseReport(w http.ResponseWriter, r *http.Request) error {
	year, month, err := parseReportYearMonth(r)
	if err != nil {
		return err
	}
	file, err := h.service.GenerateExpenseReportPDF(r.Context(), year, month)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, file.Filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Content)
	return nil
}

// GenerateIncomeReport handles GET /api/income/report?year=&month= — mirrors
// GenerateExpenseReport, see its doc comment.
func (h *Handler) GenerateIncomeReport(w http.ResponseWriter, r *http.Request) error {
	year, month, err := parseReportYearMonth(r)
	if err != nil {
		return err
	}
	file, err := h.service.GenerateIncomeReportPDF(r.Context(), year, month)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, file.Filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Content)
	return nil
}

// parseReportYearMonth reads the shared ?year=&month= query shape — year is required, month is
// optional (nil means "the whole year").
func parseReportYearMonth(r *http.Request) (year int, month *int, err error) {
	year, err = strconv.Atoi(r.URL.Query().Get("year"))
	if err != nil {
		return 0, nil, apperr.Validation("year is required and must be an integer", "year")
	}
	if raw := r.URL.Query().Get("month"); raw != "" {
		m, err := strconv.Atoi(raw)
		if err != nil {
			return 0, nil, apperr.Validation("month must be an integer", "month")
		}
		month = &m
	}
	return year, month, nil
}

// GetJob handles GET /api/jobs/{id} — mirrors JobController.status.
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	job, err := h.service.GetStatus(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, job)
	return nil
}

// DownloadJob handles GET /api/jobs/{id}/download — mirrors JobController.download. The file is
// removed from disk once served (see Service.Download) — a report downloads exactly once.
func (h *Handler) DownloadJob(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	file, err := h.service.Download(r.Context(), id)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, file.Filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Content)
	return nil
}
