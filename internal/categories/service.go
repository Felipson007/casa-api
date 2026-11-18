package categories

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Repository is defined here, at the point of consumption, per Go convention (the opposite of
// Java's JpaRepository, where the implementer declares the interface) — see
// docs/architecture-plan.md section 1. postgres.Repository implements this structurally; nothing
// in this package imports the postgres subpackage.
type Repository interface {
	List(ctx context.Context, filterType *Type) ([]Category, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Category, error)
	Create(ctx context.Context, c Category) error
	Delete(ctx context.Context, id uuid.UUID) error
	CountAll(ctx context.Context) (int, error)
}

// Service holds the business rules — the Go analog of CategoriaService. Every public method is
// O(1) database round-trips (docs/architecture-plan.md section 13): no method here loops calling
// the repository once per item of a collection.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns every category, optionally filtered by type.
func (s *Service) List(ctx context.Context, filterType *Type) ([]CategoryDTO, error) {
	cats, err := s.repo.List(ctx, filterType)
	if err != nil {
		return nil, fmt.Errorf("categories: listing: %w", err)
	}
	return toDTOs(cats), nil
}

// Create validates the parent category (if any) exists, then persists the new category.
func (s *Service) Create(ctx context.Context, req CreateCategoryRequest) (CategoryDTO, error) {
	if req.ParentCategoryID != nil {
		parent, err := s.repo.FindByID(ctx, *req.ParentCategoryID)
		if err != nil {
			return CategoryDTO{}, fmt.Errorf("categories: looking up parent: %w", err)
		}
		if parent == nil {
			return CategoryDTO{}, apperr.Validation("parentCategoryId does not reference an existing category", "parentCategoryId")
		}
	}

	category := Category{
		ID:               uuid.New(),
		Name:             req.Name,
		ParentCategoryID: req.ParentCategoryID,
		Type:             req.Type,
	}
	if err := s.repo.Create(ctx, category); err != nil {
		return CategoryDTO{}, fmt.Errorf("categories: creating: %w", err)
	}
	return toDTO(category), nil
}

// Delete removes a category by ID, or returns apperr.NotFound if it doesn't exist — mirroring
// CategoriaService's buscarEntidadeOuFalhar + ResourceNotFoundException.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("categories: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("category not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("categories: deleting: %w", err)
	}
	return nil
}

// defaultCategoryNames seeds a fresh install with a starter set — the Go analog of
// CategoriaService.criarPadroesParaTenant's self-healing seed logic, minus the per-tenant part
// (docs/architecture-plan.md section 3): this app is single-tenant now, so seeding is a one-time,
// global first-run operation, not something repeated per new tenant.
var defaultCategoryNames = map[Type][]string{
	TypeExpense: {"Moradia", "Alimentação", "Transporte"},
	TypeIncome:  {"Salário", "Outros"},
}

// SeedDefaults creates the starter categories if, and only if, none exist yet — called once, on
// first run.
func (s *Service) SeedDefaults(ctx context.Context) error {
	count, err := s.repo.CountAll(ctx)
	if err != nil {
		return fmt.Errorf("categories: counting existing: %w", err)
	}
	if count > 0 {
		return nil
	}

	for categoryType, names := range defaultCategoryNames {
		for _, name := range names {
			category := Category{
				ID:   uuid.New(),
				Name: name,
				Type: &categoryType,
			}
			if err := s.repo.Create(ctx, category); err != nil {
				return fmt.Errorf("categories: seeding default %q: %w", name, err)
			}
		}
	}
	return nil
}
