package loans

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP adapter. It never touches the repository directly.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// List handles GET /api/loans and GET /api/loans?clientId=<uuid>.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	var clientID *uuid.UUID
	if raw := r.URL.Query().Get("clientId"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return apperr.Validation("clientId must be a valid UUID", "clientId")
		}
		clientID = &parsed
	}

	dtos, err := h.service.List(r.Context(), clientID, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Get handles GET /api/loans/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	dto, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Create handles POST /api/loans.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateLoanRequest](r)
	if err != nil {
		return err
	}

	dto, err := h.service.Create(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

// Delete handles DELETE /api/loans/{id}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Update handles PUT /api/loans/{id} — full-replacement, only while the loan has no payments yet.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	req, err := httpx.DecodeAndValidate[CreateLoanRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.Update(r.Context(), id, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Cancel handles POST /api/loans/{id}/cancel.
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	dto, err := h.service.Cancel(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// ChangeDueDay handles PATCH /api/loans/{id}/due-day.
func (h *Handler) ChangeDueDay(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	req, err := httpx.DecodeAndValidate[ChangeDueDayRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.ChangeDueDay(r.Context(), id, req.DueDay)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// PostponeInstallment handles PATCH /api/loans/{loanId}/installments/{installmentId}/postpone.
func (h *Handler) PostponeInstallment(w http.ResponseWriter, r *http.Request) error {
	loanID, err := uuid.Parse(r.PathValue("loanId"))
	if err != nil {
		return apperr.Validation("loanId must be a valid UUID", "loanId")
	}
	installmentID, err := uuid.Parse(r.PathValue("installmentId"))
	if err != nil {
		return apperr.Validation("installmentId must be a valid UUID", "installmentId")
	}
	req, err := httpx.DecodeAndValidate[PostponeInstallmentRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.PostponeInstallment(r.Context(), loanID, installmentID, req.NewDueDate.Time())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// ChangeInterestRate handles PATCH /api/loans/{id}/interest-rate.
func (h *Handler) ChangeInterestRate(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	req, err := httpx.DecodeAndValidate[ChangeInterestRateRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.ChangeInterestRate(r.Context(), id, req.MonthlyInterestRate)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Vigentes handles GET /api/loans/vigentes?ano=&mes= — every relevant loan's standing for one
// calendar month (see vigentes.go).
func (h *Handler) Vigentes(w http.ResponseWriter, r *http.Request) error {
	ano, err := strconv.Atoi(r.URL.Query().Get("ano"))
	if err != nil {
		return apperr.Validation("ano is required and must be an integer", "ano")
	}
	mes, err := strconv.Atoi(r.URL.Query().Get("mes"))
	if err != nil || mes < 1 || mes > 12 {
		return apperr.Validation("mes is required and must be between 1 and 12", "mes")
	}
	month := YearMonth{Year: ano, Month: mes}
	byMonth, err := h.service.Vigentes(r.Context(), []YearMonth{month})
	if err != nil {
		return err
	}
	result := byMonth[month]
	if result == nil {
		result = []LoanSummaryDTO{}
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

// ListInstallments handles GET /api/loans/{id}/installments.
func (h *Handler) ListInstallments(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	dtos, err := h.service.ListInstallments(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// MarkInstallmentPaid handles POST /api/loans/{loanId}/installments/{installmentId}/pay.
func (h *Handler) MarkInstallmentPaid(w http.ResponseWriter, r *http.Request) error {
	loanID, err := uuid.Parse(r.PathValue("loanId"))
	if err != nil {
		return apperr.Validation("loanId must be a valid UUID", "loanId")
	}
	installmentID, err := uuid.Parse(r.PathValue("installmentId"))
	if err != nil {
		return apperr.Validation("installmentId must be a valid UUID", "installmentId")
	}

	req, err := httpx.DecodeAndValidate[MarkInstallmentPaidRequest](r)
	if err != nil {
		return err
	}

	dto, err := h.service.MarkInstallmentPaid(r.Context(), loanID, installmentID, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// ApplyAmortization handles POST /api/loans/{id}/amortize.
func (h *Handler) ApplyAmortization(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	req, err := httpx.DecodeAndValidate[ApplyAmortizationRequest](r)
	if err != nil {
		return err
	}

	dto, err := h.service.ApplyAmortization(r.Context(), id, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// RecordTransaction handles POST /api/loans/{id}/transactions.
func (h *Handler) RecordTransaction(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	req, err := httpx.DecodeAndValidate[CreateTransactionRequest](r)
	if err != nil {
		return err
	}

	dto, err := h.service.RecordTransaction(r.Context(), id, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

// ListTransactions handles GET /api/loans/{id}/transactions.
func (h *Handler) ListTransactions(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	dtos, err := h.service.ListTransactions(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// ListAllTransactions handles GET /api/transactions and GET /api/transactions?clientId=<uuid> —
// the flat, cross-loan ledger in one round trip (see loans.Repository.ListAllTransactions's doc
// comment on why this exists).
func (h *Handler) ListAllTransactions(w http.ResponseWriter, r *http.Request) error {
	var clientID *uuid.UUID
	if raw := r.URL.Query().Get("clientId"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return apperr.Validation("clientId must be a valid UUID", "clientId")
		}
		clientID = &parsed
	}

	dtos, err := h.service.ListAllTransactions(r.Context(), clientID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}
