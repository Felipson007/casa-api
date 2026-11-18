package openfinance

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/integrations"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP adapter. It never touches the repository directly.
type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// ---- DTOs ----------------------------------------------------------------------------------
// Dates are civildate.Date (see its doc: a plain time.Time would shift a day in Brazil). Money is
// a string, never a float. No DTO carries a credential or a Pluggy item id.

type ConnectionDTO struct {
	ID                uuid.UUID  `json:"id"`
	Institution       *string    `json:"institution,omitempty"`
	Status            *string    `json:"status,omitempty"`
	ExecutionStatus   *string    `json:"executionStatus,omitempty"`
	LastError         *string    `json:"lastError,omitempty"`
	NeedsAttention    bool       `json:"needsAttention"`
	ProviderUpdatedAt *time.Time `json:"providerUpdatedAt,omitempty"`
	LastSyncedAt      *time.Time `json:"lastSyncedAt,omitempty"`
	AccountCount      int        `json:"accountCount"`
}

type StatusDTO struct {
	Configured  bool            `json:"configured"`
	SyncPending bool            `json:"syncPending"`
	Connections []ConnectionDTO `json:"connections"`
}

type AccountDTO struct {
	ID              uuid.UUID `json:"id"`
	Institution     *string   `json:"institution,omitempty"`
	Type            string    `json:"type"`
	Subtype         *string   `json:"subtype,omitempty"`
	Name            *string   `json:"name,omitempty"`
	MaskedNumber    *string   `json:"maskedNumber,omitempty"`
	Balance         *string   `json:"balance,omitempty"`
	Currency        string    `json:"currency"`
	CreditLimit     *string   `json:"creditLimit,omitempty"`
	AvailableCredit *string   `json:"availableCredit,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type BillDTO struct {
	ID               uuid.UUID      `json:"id"`
	AccountID        uuid.UUID      `json:"accountId"`
	Institution      *string        `json:"institution,omitempty"`
	AccountName      *string        `json:"accountName,omitempty"`
	DueDate          civildate.Date `json:"dueDate"`
	Competence       civildate.Date `json:"competence"`
	TotalAmount      string         `json:"totalAmount"`
	MinimumPayment   *string        `json:"minimumPayment,omitempty"`
	FinanceCharges   string         `json:"financeCharges"`
	TransactionCount int            `json:"transactionCount"`
	// PaidAt: see BillView.PaidAt. Absent while the statement is not seen paid.
	PaidAt *civildate.Date `json:"paidAt,omitempty"`
}

type TransactionDTO struct {
	ID               uuid.UUID      `json:"id"`
	AccountID        uuid.UUID      `json:"accountId"`
	AccountType      string         `json:"accountType"`
	Date             civildate.Date `json:"date"`
	Amount           string         `json:"amount"`
	Direction        string         `json:"direction"`
	Status           string         `json:"status"`
	Description      string         `json:"description"`
	ProviderCategory *string        `json:"providerCategory,omitempty"`
	Kind             ProjectionKind `json:"kind"`
	Overridden       bool           `json:"overridden"`
}

type InvestmentDTO struct {
	ID               uuid.UUID       `json:"id"`
	Institution      *string         `json:"institution,omitempty"`
	Name             string          `json:"name"`
	Code             *string         `json:"code,omitempty"`
	Type             string          `json:"type"`
	Subtype          *string         `json:"subtype,omitempty"`
	Status           *string         `json:"status,omitempty"`
	Currency         string          `json:"currency"`
	Balance          string          `json:"balance"`
	AmountOriginal   *string         `json:"amountOriginal,omitempty"`
	AmountProfit     *string         `json:"amountProfit,omitempty"`
	Quantity         *string         `json:"quantity,omitempty"`
	AnnualRate       *string         `json:"annualRate,omitempty"`
	LastMonthRate    *string         `json:"lastMonthRate,omitempty"`
	Last12MonthsRate *string         `json:"last12MonthsRate,omitempty"`
	DueDate          *civildate.Date `json:"dueDate,omitempty"`
	LastSyncedAt     time.Time       `json:"lastSyncedAt"`
}

type InvestmentPointDTO struct {
	Date    civildate.Date `json:"date"`
	Balance string         `json:"balance"`
}

type SetProjectionRequest struct {
	Kind string `json:"kind" validate:"required"`
}

// ---- handlers ------------------------------------------------------------------------------

// Status handles GET /api/openfinance/status.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) error {
	rep, err := h.service.Status(r.Context())
	if err != nil {
		return err
	}
	dto := StatusDTO{Configured: rep.Configured, SyncPending: rep.SyncPending, Connections: make([]ConnectionDTO, 0, len(rep.Connections))}
	for _, c := range rep.Connections {
		inst := c.InstitutionName
		if inst == nil {
			inst = c.ConnectorName
		}
		status, exec := "", ""
		if c.Status != nil {
			status = *c.Status
		}
		if c.ExecutionStatus != nil {
			exec = *c.ExecutionStatus
		}
		dto.Connections = append(dto.Connections, ConnectionDTO{
			ID: c.ID, Institution: inst, Status: c.Status, ExecutionStatus: c.ExecutionStatus, LastError: c.LastError,
			NeedsAttention: NeedsAttention(status, exec), ProviderUpdatedAt: c.ProviderUpdatedAt,
			LastSyncedAt: c.LastSyncedAt, AccountCount: c.AccountCount,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Connect handles PUT /api/openfinance/connection — stores the Pluggy credentials (encrypted) and
// schedules the first sync. The response never echoes the secret.
func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[integrations.ConnectPluggyRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.Connect(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Sync handles POST /api/openfinance/sync — 202: the worker runs it on its next tick.
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) error {
	if err := h.service.RequestSync(r.Context()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

// Accounts handles GET /api/openfinance/accounts.
func (h *Handler) Accounts(w http.ResponseWriter, r *http.Request) error {
	views, err := h.service.Accounts(r.Context())
	if err != nil {
		return err
	}
	out := make([]AccountDTO, len(views))
	for i, v := range views {
		out[i] = AccountDTO(v)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// Bills handles GET /api/openfinance/bills.
func (h *Handler) Bills(w http.ResponseWriter, r *http.Request) error {
	views, err := h.service.Bills(r.Context())
	if err != nil {
		return err
	}
	out := make([]BillDTO, len(views))
	for i, v := range views {
		out[i] = BillDTO{ID: v.ID, AccountID: v.AccountID, Institution: v.Institution, AccountName: v.AccountName,
			DueDate: civildate.Of(v.DueDate), Competence: civildate.Of(v.Competence), TotalAmount: v.TotalAmount,
			MinimumPayment: v.MinimumPayment, FinanceCharges: v.FinanceCharges, TransactionCount: v.TransactionCount,
			PaidAt: civildate.OfPtr(v.PaidAt)}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// Transactions handles GET /api/openfinance/transactions?kind=&ignored=true&limit=&offset= — the
// audit view: what was counted, and what was deliberately left out (card payments, transfers).
func (h *Handler) Transactions(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	f := TransactionFilter{Kind: ProjectionKind(q.Get("kind")), OnlyIgnored: q.Get("ignored") == "true"}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	views, err := h.service.Transactions(r.Context(), f)
	if err != nil {
		return err
	}
	out := make([]TransactionDTO, len(views))
	for i, v := range views {
		out[i] = TransactionDTO{ID: v.ID, AccountID: v.AccountID, AccountType: v.AccountType, Date: civildate.Of(v.Date),
			Amount: v.Amount, Direction: v.Direction, Status: v.Status, Description: v.Description,
			ProviderCategory: v.ProviderCategory, Kind: v.Kind, Overridden: v.Overridden}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// SetProjection handles PUT /api/openfinance/transactions/{id}/projection.
func (h *Handler) SetProjection(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	req, err := httpx.DecodeAndValidate[SetProjectionRequest](r)
	if err != nil {
		return err
	}
	if err := h.service.SetProjection(r.Context(), id, req.Kind); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Investments handles GET /api/openfinance/investments.
func (h *Handler) Investments(w http.ResponseWriter, r *http.Request) error {
	views, err := h.service.Investments(r.Context())
	if err != nil {
		return err
	}
	out := make([]InvestmentDTO, len(views))
	for i, v := range views {
		out[i] = InvestmentDTO{ID: v.ID, Institution: v.Institution, Name: v.Name, Code: v.Code, Type: v.Type,
			Subtype: v.Subtype, Status: v.Status, Currency: v.Currency, Balance: v.Balance,
			AmountOriginal: v.AmountOriginal, AmountProfit: v.AmountProfit, Quantity: v.Quantity,
			AnnualRate: v.AnnualRate, LastMonthRate: v.LastMonthRate, Last12MonthsRate: v.Last12MonthsRate,
			DueDate: civildate.OfPtr(v.DueDate), LastSyncedAt: v.LastSyncedAt}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// InvestmentHistory handles GET /api/openfinance/investments/{id}/history.
func (h *Handler) InvestmentHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	points, err := h.service.InvestmentHistory(r.Context(), id)
	if err != nil {
		return err
	}
	out := make([]InvestmentPointDTO, len(points))
	for i, p := range points {
		out[i] = InvestmentPointDTO{Date: civildate.Of(p.Date), Balance: p.Balance}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
