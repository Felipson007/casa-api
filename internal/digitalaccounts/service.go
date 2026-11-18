package digitalaccounts

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	List(ctx context.Context, personID *uuid.UUID, category *Category, status *Status, email *string) ([]Account, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Account, error)
	Create(ctx context.Context, a Account) error
	Update(ctx context.Context, a Account) error
	MarkReviewed(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13).
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns accounts, optionally filtered by person, category, status, and/or the exact email
// address used — the last one backs "which accounts still use my old Gmail" (GAP 1's motivating
// question).
func (s *Service) List(ctx context.Context, personID *uuid.UUID, category *Category, status *Status, email *string) ([]AccountDTO, error) {
	list, err := s.repo.List(ctx, personID, category, status, email)
	if err != nil {
		return nil, fmt.Errorf("digitalaccounts: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single account, or apperr.NotFound if it doesn't exist.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (AccountDTO, error) {
	a, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return AccountDTO{}, fmt.Errorf("digitalaccounts: looking up: %w", err)
	}
	if a == nil {
		return AccountDTO{}, apperr.NotFound("digital account not found")
	}
	return toDTO(*a), nil
}

// Create persists a new account. Status always starts ACTIVE; MFAMethod defaults to NONE when the
// request omits it.
func (s *Service) Create(ctx context.Context, req CreateAccountRequest) (AccountDTO, error) {
	mfa := req.MFAMethod
	if mfa == "" {
		mfa = MFANone
	}
	a := Account{
		ID:            uuid.New(),
		PersonID:      req.PersonID,
		ServiceName:   req.ServiceName,
		Category:      req.Category,
		EmailUsed:     req.EmailUsed,
		MFAMethod:     mfa,
		RecoveryNotes: req.RecoveryNotes,
		Status:        StatusActive,
	}
	if err := s.repo.Create(ctx, a); err != nil {
		return AccountDTO{}, err // already an *apperr.Error (FK violation) or a wrapped DB error
	}
	// Re-read rather than mapping the in-memory `a` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above — same F-3 class of bug the rest of this codebase guards
	// against on every Create (see internal/assets/service.go's identical comment).
	persisted, err := s.repo.FindByID(ctx, a.ID)
	if err != nil {
		return AccountDTO{}, fmt.Errorf("digitalaccounts: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// Import creates every item that isn't already known to Atlas, in one call — the bulk endpoint
// behind the "importar" flow (browser CSV, password-manager JSON/XML). Duplicates are skipped
// per-item (same service + email, case/space-insensitive) instead of failing the request, so the
// UI can report "N importadas, M já existiam". Items within the same request are deduped too.
//
// Not atomic on purpose: a mid-batch repository error returns after the earlier creates were
// persisted, and the caller simply refetches. Skipping duplicates is inherently a per-item outcome,
// so this trades the all-or-nothing property for a useful partial result.
func (s *Service) Import(ctx context.Context, req ImportAccountsRequest) (ImportAccountsResponse, error) {
	existing, err := s.repo.List(ctx, nil, nil, nil, nil)
	if err != nil {
		return ImportAccountsResponse{}, fmt.Errorf("digitalaccounts: listing existing for import: %w", err)
	}
	seen := make(map[string]struct{}, len(existing)+len(req.Items))
	for _, a := range existing {
		seen[dedupKey(a.ServiceName, a.EmailUsed)] = struct{}{}
	}

	results := make([]ImportItemResultDTO, 0, len(req.Items))
	created, skipped := 0, 0
	for _, item := range req.Items {
		key := dedupKey(item.ServiceName, item.EmailUsed)
		if _, ok := seen[key]; ok {
			skipped++
			results = append(results, ImportItemResultDTO{
				ServiceName: strings.TrimSpace(item.ServiceName),
				EmailUsed:   item.EmailUsed,
				Outcome:     ImportOutcomeSkipped,
			})
			continue
		}
		seen[key] = struct{}{}

		mfa := item.MFAMethod
		if mfa == "" {
			mfa = MFANone
		}
		a := Account{
			ID:            uuid.New(),
			ServiceName:   strings.TrimSpace(item.ServiceName),
			Category:      item.Category,
			EmailUsed:     item.EmailUsed,
			MFAMethod:     mfa,
			RecoveryNotes: item.RecoveryNotes,
			Status:        StatusActive,
		}
		if err := s.repo.Create(ctx, a); err != nil {
			return ImportAccountsResponse{}, err
		}
		created++
		results = append(results, ImportItemResultDTO{
			ServiceName: a.ServiceName,
			EmailUsed:   a.EmailUsed,
			Outcome:     ImportOutcomeCreated,
		})
	}
	return ImportAccountsResponse{Created: created, Skipped: skipped, Items: results}, nil
}

// dedupKey identifies "the same account" across an export file and what Atlas already has: the
// service name plus the email, trimmed and lower-cased, so "GitHub"/" github " and a differing
// email case don't slip a second copy in. A missing email dedups on the service name alone.
func dedupKey(serviceName string, email *string) string {
	key := strings.ToLower(strings.TrimSpace(serviceName))
	if email == nil {
		return key
	}
	return key + "\x00" + strings.ToLower(strings.TrimSpace(*email))
}

// Update replaces an account's editable fields (including Status), or returns apperr.NotFound if
// it doesn't exist. LastReviewedAt is preserved as-is — see MarkReviewed for the only way to set
// it.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateAccountRequest) (AccountDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return AccountDTO{}, fmt.Errorf("digitalaccounts: looking up for update: %w", err)
	}
	if existing == nil {
		return AccountDTO{}, apperr.NotFound("digital account not found")
	}

	updated := Account{
		ID:             id,
		PersonID:       req.PersonID,
		ServiceName:    req.ServiceName,
		Category:       req.Category,
		EmailUsed:      req.EmailUsed,
		MFAMethod:      req.MFAMethod,
		RecoveryNotes:  req.RecoveryNotes,
		Status:         req.Status,
		LastReviewedAt: existing.LastReviewedAt,
		CreatedAt:      existing.CreatedAt,
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return AccountDTO{}, err
	}
	return toDTO(updated), nil
}

// MarkReviewed sets LastReviewedAt to now, or returns apperr.NotFound if the account doesn't
// exist. Backs POST /api/digital-accounts/{id}/mark-reviewed — the "I looked at this, it's fine
// for now" action the Mapa da Vida's "N não revisadas" count is driven by.
func (s *Service) MarkReviewed(ctx context.Context, id uuid.UUID) (AccountDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return AccountDTO{}, fmt.Errorf("digitalaccounts: looking up for mark-reviewed: %w", err)
	}
	if existing == nil {
		return AccountDTO{}, apperr.NotFound("digital account not found")
	}
	if err := s.repo.MarkReviewed(ctx, id); err != nil {
		return AccountDTO{}, fmt.Errorf("digitalaccounts: marking reviewed: %w", err)
	}
	persisted, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return AccountDTO{}, fmt.Errorf("digitalaccounts: re-reading after mark-reviewed: %w", err)
	}
	return toDTO(*persisted), nil
}

// Delete removes an account by ID, or returns apperr.NotFound if it doesn't exist.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("digitalaccounts: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("digital account not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("digitalaccounts: deleting: %w", err)
	}
	return nil
}
