package finance

import (
	"context"
	"fmt"
	"math/big"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	List(ctx context.Context) ([]Balance, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Balance, error)
	Create(ctx context.Context, b Balance) error
	// Update persists b. When snapshot is non-nil, it's inserted into
	// financial_balance_history in the SAME transaction as the update — mirrors the atomicity of
	// the old Java @Transactional SaldoFinanceiroService.atualizar (one DB round trip, not two).
	Update(ctx context.Context, b Balance, snapshot *BalanceHistoryEntry) error
	Delete(ctx context.Context, id uuid.UUID) error
	ListHistory(ctx context.Context, balanceID uuid.UUID) ([]BalanceHistoryEntry, error)
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13).
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns every financial balance, ordered by name — mirrors the old Java
// SaldoFinanceiroRepository.findAllByOrderByNomeAsc.
func (s *Service) List(ctx context.Context) ([]BalanceDTO, error) {
	list, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("finance: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Create persists a new balance.
func (s *Service) Create(ctx context.Context, req CreateBalanceRequest) (BalanceDTO, error) {
	b := Balance{
		ID:           uuid.New(),
		PersonID:     req.PersonID,
		Name:         req.Name,
		Type:         req.Type,
		CurrentValue: req.CurrentValue,
		UpdatedOn:    req.UpdatedOn.Time(),
		Notes:        req.Notes,
	}
	if err := s.repo.Create(ctx, b); err != nil {
		return BalanceDTO{}, err // already an *apperr.Error (FK violation) or a wrapped DB error
	}
	// Re-read rather than mapping the in-memory `b` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above (found systemically across every Create in this codebase by
	// Hermes's HTTP endpoint sweep on loans, msg 83 F-3).
	persisted, err := s.repo.FindByID(ctx, b.ID)
	if err != nil {
		return BalanceDTO{}, fmt.Errorf("finance: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// Update replaces a balance's editable fields. If CurrentValue or UpdatedOn actually changed, the
// PREVIOUS value is snapshotted into financial_balance_history first — mirrors the old Java
// SaldoFinanceiroService.atualizar exactly, including skipping the snapshot on a no-op edit (e.g.
// only Name/Notes changed) to avoid history noise from edits that didn't touch the value.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateBalanceRequest) (BalanceDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return BalanceDTO{}, fmt.Errorf("finance: looking up for update: %w", err)
	}
	if existing == nil {
		return BalanceDTO{}, apperr.NotFound("financial balance not found")
	}

	valueChanged, err := amountsDiffer(existing.CurrentValue, req.CurrentValue)
	if err != nil {
		return BalanceDTO{}, apperr.Validation("currentValue must be numeric", "currentValue")
	}
	dateChanged := !existing.UpdatedOn.Equal(req.UpdatedOn.Time())

	var snapshot *BalanceHistoryEntry
	if valueChanged || dateChanged {
		snapshot = &BalanceHistoryEntry{
			ID:         uuid.New(),
			BalanceID:  id,
			Balance:    existing.CurrentValue,
			RecordedOn: existing.UpdatedOn,
		}
	}

	updated := Balance{
		ID:           id,
		PersonID:     req.PersonID,
		Name:         req.Name,
		Type:         req.Type,
		CurrentValue: req.CurrentValue,
		UpdatedOn:    req.UpdatedOn.Time(),
		Notes:        req.Notes,
		CreatedAt:    existing.CreatedAt,
	}
	if existing.BankAccountID != nil {
		// Synced from the bank: name/person/notes stay editable, the value and its date do not
		// (the next sync would overwrite them anyway) — and no history snapshot is warranted.
		updated.CurrentValue, updated.UpdatedOn, updated.BankAccountID = existing.CurrentValue, existing.UpdatedOn, existing.BankAccountID
		snapshot = nil
	}
	if err := s.repo.Update(ctx, updated, snapshot); err != nil {
		return BalanceDTO{}, err
	}
	// Re-read rather than mapping `updated` directly: CurrentValue is echoed back at whatever
	// scale the caller sent it, not the numeric(15,2) canonical form Postgres actually stores —
	// same class of bug as F-3 (Hermes's HTTP endpoint sweep, msg 83), just on Update instead of
	// Create.
	persisted, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return BalanceDTO{}, fmt.Errorf("finance: re-reading after update: %w", err)
	}
	return toDTO(*persisted), nil
}

// Delete removes a balance by ID (its history rows cascade via ON DELETE CASCADE), or returns
// apperr.NotFound if it doesn't exist.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("finance: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("financial balance not found")
	}
	if existing.BankAccountID != nil {
		return apperr.Validation("this balance is synced from a bank account; disconnect the connection instead of deleting it")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("finance: deleting: %w", err)
	}
	return nil
}

// ListHistory returns a balance's value timeline, most recent first — mirrors the old Java
// SaldoFinanceiroService.listarHistorico. Returns apperr.NotFound if the balance itself doesn't
// exist (an empty history for a real balance is not an error — it just means it's never been
// edited since creation).
func (s *Service) ListHistory(ctx context.Context, balanceID uuid.UUID) ([]BalanceHistoryEntryDTO, error) {
	existing, err := s.repo.FindByID(ctx, balanceID)
	if err != nil {
		return nil, fmt.Errorf("finance: looking up for history: %w", err)
	}
	if existing == nil {
		return nil, apperr.NotFound("financial balance not found")
	}
	list, err := s.repo.ListHistory(ctx, balanceID)
	if err != nil {
		return nil, fmt.Errorf("finance: listing history: %w", err)
	}
	return toHistoryDTOs(list), nil
}

// amountsDiffer compares two money-as-string values for real numeric inequality (not string
// inequality — "100" and "100.00" are the same amount) using math/big.Rat, avoiding float
// rounding without adding a decimal-library dependency (docs/architecture-plan.md section 3).
func amountsDiffer(a, b string) (bool, error) {
	ra, ok := new(big.Rat).SetString(a)
	if !ok {
		return false, fmt.Errorf("finance: %q is not a valid decimal", a)
	}
	rb, ok := new(big.Rat).SetString(b)
	if !ok {
		return false, fmt.Errorf("finance: %q is not a valid decimal", b)
	}
	return ra.Cmp(rb) != 0, nil
}
