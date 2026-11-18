package expenses

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

// originalDateTime converts a request's *civildate.Date into the *time.Time the domain struct
// still uses internally — nil-safe, since OriginalDate is optional on both Create and Update.
func originalDateTime(d *civildate.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
//
// Create/Update's FK violations (categoryId/personId/documentId not found) are translated to
// *apperr.Error by the postgres implementation — see internal/expenses/postgres/repository.go.
type Repository interface {
	List(ctx context.Context, categoryID, personID *uuid.UUID, query string) ([]Expense, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Expense, error)
	Create(ctx context.Context, e Expense) error
	Update(ctx context.Context, e Expense) error
	Delete(ctx context.Context, id uuid.UUID) error

	// ListByDocument returns every expense linked to one document (an imported invoice's line
	// items), ordered by original_date. ReplaceForDocument atomically deletes all of a document's
	// existing lines and inserts es in their place — one transaction, so a re-extraction of the
	// same invoice can never leave a half-imported statement. Both back documents.Service's
	// itemized invoice extraction + reconciliation (see internal/documents/extract.go).
	ListByDocument(ctx context.Context, documentID uuid.UUID) ([]Expense, error)
	ReplaceForDocument(ctx context.Context, documentID uuid.UUID, es []Expense) error

	// ListInRange/ListRecurringSince back the reporting methods in reports.go — see that file's
	// doc comments for exactly what each is used for.
	ListInRange(ctx context.Context, from, toExclusive time.Time) ([]ExpenseWithCategoryName, error)
	ListRecurringSince(ctx context.Context, since time.Time) ([]ExpenseWithCategoryName, error)

	// CategoryHistory returns every classified expense as description + category name + manual
	// flag — the raw material the category memory (internal/categorize) learns from.
	CategoryHistory(ctx context.Context) ([]categorize.Example, error)
	// CategoryIDsByName returns a lower(name)->id lookup for categories of one type, so
	// autocategorisation can turn the memory's category names into IDs in one round-trip.
	CategoryIDsByName(ctx context.Context, categoryType string) (map[string]uuid.UUID, error)
	// AutoCategorize applies assigns in one transaction, never touching a MANUAL row. Returns how
	// many rows were actually filled.
	AutoCategorize(ctx context.Context, assigns []CategoryAssignment) (int, error)
}

// CategoryAssignment is one autocategorisation decision: expense ID -> category ID.
type CategoryAssignment struct {
	ID         uuid.UUID
	CategoryID uuid.UUID
}

// categorySourceForNew picks the provenance for a brand-new row: an explicit source carried by an
// internal caller (documents carries AI/HISTORY from its review draft) wins; otherwise a category
// chosen on a hand-created row is MANUAL, and no category is NONE.
func categorySourceForNew(categoryID *uuid.UUID, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if categoryID == nil {
		return CategorySourceNone
	}
	return CategorySourceManual
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13).
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns expenses, optionally filtered by category, person and/or a description substring
// (case-insensitive).
func (s *Service) List(ctx context.Context, categoryID, personID *uuid.UUID, query string) ([]ExpenseDTO, error) {
	list, err := s.repo.List(ctx, categoryID, personID, query)
	if err != nil {
		return nil, fmt.Errorf("expenses: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single expense, or apperr.NotFound if it doesn't exist.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (ExpenseDTO, error) {
	e, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return ExpenseDTO{}, fmt.Errorf("expenses: looking up: %w", err)
	}
	if e == nil {
		return ExpenseDTO{}, apperr.NotFound("expense not found")
	}
	return toDTO(*e), nil
}

// Create persists a new expense. Reviewed always starts false — an expense is only ever marked
// reviewed later, via Update.
func (s *Service) Create(ctx context.Context, req CreateExpenseRequest) (ExpenseDTO, error) {
	e := Expense{
		ID:             uuid.New(),
		DocumentID:     req.DocumentID,
		CategoryID:     req.CategoryID,
		PersonID:       req.PersonID,
		Amount:         req.Amount,
		ExpenseDate:    req.ExpenseDate.Time(),
		Description:    req.Description,
		Reviewed:       false,
		OriginalDate:   originalDateTime(req.OriginalDate),
		Recurring:      req.Recurring,
		CategorySource: categorySourceForNew(req.CategoryID, req.CategorySource),
	}
	if err := s.repo.Create(ctx, e); err != nil {
		return ExpenseDTO{}, err // already an *apperr.Error (FK violation) or a wrapped DB error
	}
	// Re-read rather than mapping the in-memory `e` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above (found systemically across every Create in this codebase by
	// Hermes's HTTP endpoint sweep on loans, msg 83 F-3).
	persisted, err := s.repo.FindByID(ctx, e.ID)
	if err != nil {
		return ExpenseDTO{}, fmt.Errorf("expenses: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// Update replaces an expense's editable fields (including Reviewed), or returns apperr.NotFound
// if it doesn't exist.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateExpenseRequest) (ExpenseDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return ExpenseDTO{}, fmt.Errorf("expenses: looking up for update: %w", err)
	}
	if existing == nil {
		return ExpenseDTO{}, apperr.NotFound("expense not found")
	}

	updated := Expense{
		ID:           id,
		DocumentID:   req.DocumentID,
		CategoryID:   req.CategoryID,
		PersonID:     req.PersonID,
		Amount:       req.Amount,
		ExpenseDate:  req.ExpenseDate.Time(),
		Description:  req.Description,
		Reviewed:     req.Reviewed,
		OriginalDate: originalDateTime(req.OriginalDate),
		Recurring:    req.Recurring,
		CreatedAt:    existing.CreatedAt,
	}
	// A category the human changed is authoritative from now on: it becomes MANUAL and feeds the
	// memory, so later launches of the same merchant inherit it. Clearing it drops back to NONE.
	// It also counts as a review, which protects the row from a later bank sync retracting it.
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
		updated.ExpenseDate = existing.ExpenseDate
		updated.OriginalDate = existing.OriginalDate
		updated.DocumentID = existing.DocumentID
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return ExpenseDTO{}, err
	}
	// Re-read rather than mapping `updated` directly: Amount is echoed back at whatever scale the
	// caller sent it, not the numeric(15,2) canonical form Postgres actually stores (e.g. "100"
	// vs "100.00") — same class of bug as F-3 (Hermes's HTTP endpoint sweep, msg 83), just on
	// Update instead of Create.
	persisted, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return ExpenseDTO{}, fmt.Errorf("expenses: re-reading after update: %w", err)
	}
	return toDTO(*persisted), nil
}

// ListByDocument returns every expense line imported from one document (invoice), ordered by
// original purchase date.
func (s *Service) ListByDocument(ctx context.Context, documentID uuid.UUID) ([]ExpenseDTO, error) {
	list, err := s.repo.ListByDocument(ctx, documentID)
	if err != nil {
		return nil, fmt.Errorf("expenses: listing by document: %w", err)
	}
	return toDTOs(list), nil
}

// ReplaceForDocument atomically replaces every expense line of one document with reqs — used by
// documents.Service when it (re-)extracts an invoice: the whole statement is re-imported as one
// unit rather than diffed line by line. Reviewed always starts false, same as Create. Returns the
// number of lines written.
func (s *Service) ReplaceForDocument(ctx context.Context, documentID uuid.UUID, reqs []CreateExpenseRequest) (int, error) {
	lines := make([]Expense, 0, len(reqs))
	for _, req := range reqs {
		lines = append(lines, Expense{
			ID:             uuid.New(),
			DocumentID:     &documentID,
			CategoryID:     req.CategoryID,
			PersonID:       req.PersonID,
			Amount:         req.Amount,
			ExpenseDate:    req.ExpenseDate.Time(),
			Description:    req.Description,
			Reviewed:       false,
			OriginalDate:   originalDateTime(req.OriginalDate),
			Recurring:      req.Recurring,
			CategorySource: categorySourceForNew(req.CategoryID, req.CategorySource),
		})
	}
	if err := s.repo.ReplaceForDocument(ctx, documentID, lines); err != nil {
		return 0, err
	}
	return len(lines), nil
}

// Delete removes an expense by ID, or returns apperr.NotFound if it doesn't exist.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("expenses: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("expense not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("expenses: deleting: %w", err)
	}
	return nil
}

// CategoryHistory returns the household's classified expenses as the examples the category memory
// learns from — used by documents' invoice extraction (to prompt and to inherit) and by
// Autocategorize.
func (s *Service) CategoryHistory(ctx context.Context) ([]categorize.Example, error) {
	examples, err := s.repo.CategoryHistory(ctx)
	if err != nil {
		return nil, fmt.Errorf("expenses: loading category history: %w", err)
	}
	return examples, nil
}

// Uncategorized returns every expense with no category — the worklist the frontend's manual
// categorisation modal shows. A low-confidence autocategorisation deliberately leaves rows here
// (see Autocategorize).
func (s *Service) Uncategorized(ctx context.Context) ([]ExpenseDTO, error) {
	list, err := s.repo.List(ctx, nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("expenses: listing uncategorized: %w", err)
	}
	result := make([]ExpenseDTO, 0)
	for _, e := range list {
		if e.CategoryID == nil {
			result = append(result, toDTO(e))
		}
	}
	return result, nil
}

// Autocategorize fills the category of every expense that has none, from the household's own
// category memory (same/similar description already classified). It never touches a row a human
// classified (MANUAL) nor one already categorised by AI/RULE/HISTORY, and returns how many rows it
// filled. Rows with no confident match are left for review.
func (s *Service) Autocategorize(ctx context.Context) (int, error) {
	examples, err := s.repo.CategoryHistory(ctx)
	if err != nil {
		return 0, fmt.Errorf("expenses: autocategorize history: %w", err)
	}
	memory := categorize.NewMemory(examples)
	if memory.Len() == 0 {
		return 0, nil
	}
	categoryIDs, err := s.repo.CategoryIDsByName(ctx, "EXPENSE")
	if err != nil {
		return 0, fmt.Errorf("expenses: autocategorize category lookup: %w", err)
	}
	list, err := s.repo.List(ctx, nil, nil, "")
	if err != nil {
		return 0, fmt.Errorf("expenses: autocategorize listing: %w", err)
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
			// Weak/low-confidence matches are left uncategorised on purpose, so the review modal
			// asks the human instead of silently filing a guess (see Uncategorized).
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
		return 0, fmt.Errorf("expenses: autocategorizing: %w", err)
	}
	return updated, nil
}
