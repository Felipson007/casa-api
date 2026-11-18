package income

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/categorize"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// CategoryAssignment is one autocategorisation decision: entry ID -> category ID.
type CategoryAssignment struct {
	ID         uuid.UUID
	CategoryID uuid.UUID
}

// categorySourceForNew mirrors expenses' helper: an explicit internal source wins, a hand-created
// row with a category is MANUAL, and no category is NONE.
func categorySourceForNew(categoryID *uuid.UUID, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if categoryID == nil {
		return CategorySourceNone
	}
	return CategorySourceManual
}

// originalDateTime converts a request's *civildate.Date into the *time.Time the domain struct
// still uses internally — nil-safe, since OriginalDate is optional on both Create and Update.
func originalDateTime(d *civildate.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}

// validateUSDPair rejects a request that sets exactly one of USDAmount/ExchangeRate — they're only
// meaningful together (see Entry's doc comment); one without the other would either silently do
// nothing useful (ExchangeRate is only read alongside USDAmount everywhere else in this package) or
// look like a foreign-currency entry it isn't.
func validateUSDPair(usdAmount, exchangeRate *string) error {
	if (usdAmount == nil) != (exchangeRate == nil) {
		return apperr.Validation("usdAmount and exchangeRate must be given together", "usdAmount")
	}
	return nil
}

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	List(ctx context.Context, categoryID, personID *uuid.UUID, query string) ([]Entry, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Entry, error)
	Create(ctx context.Context, e Entry) error
	Update(ctx context.Context, e Entry) error
	Delete(ctx context.Context, id uuid.UUID) error

	// ListByDocument / ReplaceForDocument back documents.Service's itemized paystub extraction —
	// same contract as their expenses counterparts: a paystub is re-imported as one unit (all its
	// provento/desconto lines together), never diffed line by line.
	ListByDocument(ctx context.Context, documentID uuid.UUID) ([]Entry, error)
	ReplaceForDocument(ctx context.Context, documentID uuid.UUID, es []Entry) error

	// ListInRange backs the reporting methods in reports.go.
	ListInRange(ctx context.Context, from, toExclusive time.Time) ([]EntryWithCategoryName, error)

	// CategoryHistory/CategoryIDsByName/AutoCategorize back the category memory and
	// autocategorisation — see internal/expenses/service.go for the same contract.
	CategoryHistory(ctx context.Context) ([]categorize.Example, error)
	CategoryIDsByName(ctx context.Context, categoryType string) (map[string]uuid.UUID, error)
	AutoCategorize(ctx context.Context, assigns []CategoryAssignment) (int, error)
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13).
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns income entries, optionally filtered by category, person and/or a description
// substring (case-insensitive).
func (s *Service) List(ctx context.Context, categoryID, personID *uuid.UUID, query string) ([]EntryDTO, error) {
	list, err := s.repo.List(ctx, categoryID, personID, query)
	if err != nil {
		return nil, fmt.Errorf("income: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single income entry, or apperr.NotFound if it doesn't exist.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (EntryDTO, error) {
	e, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return EntryDTO{}, fmt.Errorf("income: looking up: %w", err)
	}
	if e == nil {
		return EntryDTO{}, apperr.NotFound("income entry not found")
	}
	return toDTO(*e), nil
}

// Create persists a new income entry. Reviewed always starts false.
func (s *Service) Create(ctx context.Context, req CreateEntryRequest) (EntryDTO, error) {
	if err := validateUSDPair(req.USDAmount, req.ExchangeRate); err != nil {
		return EntryDTO{}, err
	}
	e := Entry{
		ID:             uuid.New(),
		DocumentID:     req.DocumentID,
		CategoryID:     req.CategoryID,
		PersonID:       req.PersonID,
		Amount:         req.Amount,
		IncomeDate:     req.IncomeDate.Time(),
		OriginalDate:   originalDateTime(req.OriginalDate),
		Description:    req.Description,
		Reviewed:       false,
		USDAmount:      req.USDAmount,
		ExchangeRate:   req.ExchangeRate,
		CategorySource: categorySourceForNew(req.CategoryID, req.CategorySource),
	}
	if err := s.repo.Create(ctx, e); err != nil {
		return EntryDTO{}, err // already an *apperr.Error (FK violation) or a wrapped DB error
	}
	// Re-read rather than mapping the in-memory `e` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above (found systemically across every Create in this codebase by
	// Hermes's HTTP endpoint sweep on loans, msg 83 F-3).
	persisted, err := s.repo.FindByID(ctx, e.ID)
	if err != nil {
		return EntryDTO{}, fmt.Errorf("income: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// Update replaces an income entry's editable fields (including Reviewed), or returns
// apperr.NotFound if it doesn't exist.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateEntryRequest) (EntryDTO, error) {
	if err := validateUSDPair(req.USDAmount, req.ExchangeRate); err != nil {
		return EntryDTO{}, err
	}
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return EntryDTO{}, fmt.Errorf("income: looking up for update: %w", err)
	}
	if existing == nil {
		return EntryDTO{}, apperr.NotFound("income entry not found")
	}

	updated := Entry{
		ID:           id,
		DocumentID:   req.DocumentID,
		CategoryID:   req.CategoryID,
		PersonID:     req.PersonID,
		Amount:       req.Amount,
		IncomeDate:   req.IncomeDate.Time(),
		OriginalDate: originalDateTime(req.OriginalDate),
		Description:  req.Description,
		Reviewed:     req.Reviewed,
		CreatedAt:    existing.CreatedAt,
		USDAmount:    req.USDAmount,
		ExchangeRate: req.ExchangeRate,
	}
	// A category the human changed becomes MANUAL (authoritative) and counts as a review, which
	// protects the row from a later bank sync retracting it. Clearing it drops to NONE.
	updated.CategorySource = existing.CategorySource
	switch {
	case req.CategoryID == nil:
		updated.CategorySource = CategorySourceNone
	case existing.CategoryID == nil || *existing.CategoryID != *req.CategoryID:
		updated.CategorySource = CategorySourceManual
		updated.Reviewed = true
	}
	if existing.Source == SourcePluggy {
		// What the bank reported is not editable; the household still classifies and reviews it.
		updated.Amount = existing.Amount
		updated.IncomeDate = existing.IncomeDate
		updated.OriginalDate = existing.OriginalDate
		updated.DocumentID = existing.DocumentID
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return EntryDTO{}, err
	}
	// Re-read rather than mapping `updated` directly: Amount is echoed back at whatever scale the
	// caller sent it, not the numeric(15,2) canonical form Postgres actually stores — same class
	// of bug as F-3 (Hermes's HTTP endpoint sweep, msg 83), just on Update instead of Create.
	persisted, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return EntryDTO{}, fmt.Errorf("income: re-reading after update: %w", err)
	}
	return toDTO(*persisted), nil
}

// ListByDocument returns every income line imported from one document (paystub), ordered by
// original date.
func (s *Service) ListByDocument(ctx context.Context, documentID uuid.UUID) ([]EntryDTO, error) {
	list, err := s.repo.ListByDocument(ctx, documentID)
	if err != nil {
		return nil, fmt.Errorf("income: listing by document: %w", err)
	}
	return toDTOs(list), nil
}

// ReplaceForDocument atomically replaces every income line of one document with reqs — used by
// documents.Service when it (re-)extracts a paystub into its provento/desconto lines. Reviewed
// always starts false. Returns the number of lines written.
func (s *Service) ReplaceForDocument(ctx context.Context, documentID uuid.UUID, reqs []CreateEntryRequest) (int, error) {
	lines := make([]Entry, 0, len(reqs))
	for _, req := range reqs {
		lines = append(lines, Entry{
			ID:             uuid.New(),
			DocumentID:     &documentID,
			CategoryID:     req.CategoryID,
			PersonID:       req.PersonID,
			Amount:         req.Amount,
			IncomeDate:     req.IncomeDate.Time(),
			OriginalDate:   originalDateTime(req.OriginalDate),
			Description:    req.Description,
			Reviewed:       false,
			USDAmount:      req.USDAmount,
			ExchangeRate:   req.ExchangeRate,
			CategorySource: categorySourceForNew(req.CategoryID, req.CategorySource),
		})
	}
	if err := s.repo.ReplaceForDocument(ctx, documentID, lines); err != nil {
		return 0, err
	}
	return len(lines), nil
}

// Delete removes an income entry by ID, or returns apperr.NotFound if it doesn't exist.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("income: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("income entry not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("income: deleting: %w", err)
	}
	return nil
}

// CategoryHistory returns the household's classified entries as category-memory examples — used by
// the Open Finance projection (for credits) and by Autocategorize.
func (s *Service) CategoryHistory(ctx context.Context) ([]categorize.Example, error) {
	examples, err := s.repo.CategoryHistory(ctx)
	if err != nil {
		return nil, fmt.Errorf("income: loading category history: %w", err)
	}
	return examples, nil
}

// Uncategorized returns every income entry with no category — the worklist for the manual
// categorisation modal.
func (s *Service) Uncategorized(ctx context.Context) ([]EntryDTO, error) {
	list, err := s.repo.List(ctx, nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("income: listing uncategorized: %w", err)
	}
	result := make([]EntryDTO, 0)
	for _, e := range list {
		if e.CategoryID == nil {
			result = append(result, toDTO(e))
		}
	}
	return result, nil
}

// Autocategorize fills the category of every uncategorised, non-MANUAL income entry from the
// household's own category memory. Returns how many rows it filled.
func (s *Service) Autocategorize(ctx context.Context) (int, error) {
	examples, err := s.repo.CategoryHistory(ctx)
	if err != nil {
		return 0, fmt.Errorf("income: autocategorize history: %w", err)
	}
	memory := categorize.NewMemory(examples)
	if memory.Len() == 0 {
		return 0, nil
	}
	categoryIDs, err := s.repo.CategoryIDsByName(ctx, "INCOME")
	if err != nil {
		return 0, fmt.Errorf("income: autocategorize category lookup: %w", err)
	}
	list, err := s.repo.List(ctx, nil, nil, "")
	if err != nil {
		return 0, fmt.Errorf("income: autocategorize listing: %w", err)
	}
	assigns := make([]CategoryAssignment, 0)
	for _, e := range list {
		if e.Description == nil || e.CategorySource == CategorySourceManual {
			continue
		}
		if e.CategoryID != nil && e.CategorySource != CategorySourceNone {
			continue
		}
		suggestion, ok := memory.Suggest(*e.Description)
		if !ok || !suggestion.Strong {
			// Weak/low-confidence matches are left uncategorised for manual review — same rule as
			// expenses.Autocategorize.
			continue
		}
		categoryID, ok := categoryIDs[strings.ToLower(strings.TrimSpace(suggestion.Category))]
		if !ok {
			continue
		}
		assigns = append(assigns, CategoryAssignment{ID: e.ID, CategoryID: categoryID})
	}
	if len(assigns) == 0 {
		return 0, nil
	}
	updated, err := s.repo.AutoCategorize(ctx, assigns)
	if err != nil {
		return 0, fmt.Errorf("income: autocategorizing: %w", err)
	}
	return updated, nil
}
