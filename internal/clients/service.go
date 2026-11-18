package clients

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom. postgres.Repository implements this
// structurally; nothing in this package imports the postgres subpackage.
type Repository interface {
	List(ctx context.Context, activeOnly *bool, query string) ([]Client, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Client, error)
	Create(ctx context.Context, c Client) error
	Update(ctx context.Context, c Client) error
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

// List returns clients, optionally filtered to only active ones (activeOnly == true) or only
// inactive ones (activeOnly == false); nil returns everyone.
func (s *Service) List(ctx context.Context, activeOnly *bool, query string) ([]ClientDTO, error) {
	list, err := s.repo.List(ctx, activeOnly, query)
	if err != nil {
		return nil, fmt.Errorf("clients: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single client, or apperr.NotFound if it doesn't exist.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (ClientDTO, error) {
	c, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return ClientDTO{}, fmt.Errorf("clients: looking up: %w", err)
	}
	if c == nil {
		return ClientDTO{}, apperr.NotFound("client not found")
	}
	return toDTO(*c), nil
}

// Create persists a new client. Active always starts true — a client is only ever deactivated
// later via Update, never created pre-deactivated.
func (s *Service) Create(ctx context.Context, req CreateClientRequest) (ClientDTO, error) {
	c := Client{
		ID:      uuid.New(),
		Name:    req.Name,
		Phone:   req.Phone,
		Email:   req.Email,
		CPFCNPJ: req.CPFCNPJ,
		Notes:   req.Notes,
		Active:  true,
		PixKey:  req.PixKey,
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return ClientDTO{}, fmt.Errorf("clients: creating: %w", err)
	}
	// Re-read rather than mapping the in-memory `c` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above — mapping `c` would return the zero time in the response
	// (found systemically across every Create in this codebase by Hermes's HTTP endpoint sweep on
	// loans, msg 83 F-3; GET immediately after Create already returned the correct value).
	persisted, err := s.repo.FindByID(ctx, c.ID)
	if err != nil {
		return ClientDTO{}, fmt.Errorf("clients: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// Update replaces a client's editable fields (including Active — see UpdateClientRequest's doc),
// or returns apperr.NotFound if the client doesn't exist.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateClientRequest) (ClientDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return ClientDTO{}, fmt.Errorf("clients: looking up for update: %w", err)
	}
	if existing == nil {
		return ClientDTO{}, apperr.NotFound("client not found")
	}

	updated := Client{
		ID:        id,
		Name:      req.Name,
		Phone:     req.Phone,
		Email:     req.Email,
		CPFCNPJ:   req.CPFCNPJ,
		Notes:     req.Notes,
		Active:    req.Active,
		PixKey:    req.PixKey,
		CreatedAt: existing.CreatedAt,
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return ClientDTO{}, fmt.Errorf("clients: updating: %w", err)
	}
	return toDTO(updated), nil
}

// Delete removes a client by ID, or returns apperr.NotFound if it doesn't exist. Loans reference
// clients with ON DELETE CASCADE (000001_init_schema.up.sql) — a client with existing loans loses
// that loan history on delete. Prefer Update(active=false) over Delete for a client that has any
// loan history; Delete is meant for a client created by mistake with nothing attached yet.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("clients: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("client not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("clients: deleting: %w", err)
	}
	return nil
}
