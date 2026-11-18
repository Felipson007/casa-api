package jobexperience

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/documents"
	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// isNotFound reports whether err is an apperr.Error of KindNotFound — used to turn a dependency
// service's own 404 into this package's own apperr.NotFound with a message naming what wasn't
// found, same idiom as internal/personinfo.
func isNotFound(err error) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Kind == apperr.KindNotFound
}

// endDateTime converts a request's optional *civildate.Date into the *time.Time the domain struct
// uses internally — nil-safe (nil EndDate means "current job").
func endDateTime(d *civildate.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}

// validateDates rejects an EndDate before StartDate — mirrors the DB's own CHECK constraint
// (migration 000020) so the caller gets a clean 400 instead of an opaque SQL error, same pattern as
// internal/people's self-relation check.
func validateDates(start time.Time, end *time.Time) error {
	if end != nil && end.Before(start) {
		return apperr.Validation("endDate cannot be before startDate", "endDate")
	}
	return nil
}

// contractTypeOrDefault treats an omitted contractType (empty string — older clients, or a request
// that just doesn't set it) as CLT, matching the column's own DB DEFAULT (migration 000021) so
// there's no split-brain between "not sent" and "explicitly CLT".
func contractTypeOrDefault(raw string) ContractType {
	if raw == string(ContractPJ) {
		return ContractPJ
	}
	return ContractCLT
}

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	List(ctx context.Context, personID uuid.UUID) ([]Experience, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Experience, error)
	Create(ctx context.Context, e Experience) error
	Update(ctx context.Context, e Experience) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// Service holds the business rules. people/documents are the concrete sibling services (same
// composition pattern as internal/personinfo.Service) — Create/Update only ever call their existing
// Get methods to check a referenced person/résumé exists, translating a NotFound into this
// package's own.
type Service struct {
	repo      Repository
	people    *people.Service
	documents *documents.Service
}

func NewService(repo Repository, peopleSvc *people.Service, documentsSvc *documents.Service) *Service {
	return &Service{repo: repo, people: peopleSvc, documents: documentsSvc}
}

// checkPersonExists 404s (as this package's own apperr.NotFound) if personID doesn't resolve.
func (s *Service) checkPersonExists(ctx context.Context, personID uuid.UUID) error {
	if _, err := s.people.GetPerson(ctx, personID); err != nil {
		if isNotFound(err) {
			return apperr.NotFound("person not found")
		}
		return fmt.Errorf("jobexperience: looking up person: %w", err)
	}
	return nil
}

// checkResumeExists 404s if a non-nil resumeDocumentID doesn't resolve to a real document — a no-op
// for nil (résumé is optional).
func (s *Service) checkResumeExists(ctx context.Context, resumeDocumentID *uuid.UUID) error {
	if resumeDocumentID == nil {
		return nil
	}
	if _, err := s.documents.Get(ctx, *resumeDocumentID); err != nil {
		if isNotFound(err) {
			return apperr.NotFound("resume document not found")
		}
		return fmt.Errorf("jobexperience: looking up resume document: %w", err)
	}
	return nil
}

// List returns a person's job experiences, most recent first (see postgres.Repository.List).
func (s *Service) List(ctx context.Context, personID uuid.UUID) ([]ExperienceDTO, error) {
	list, err := s.repo.List(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("jobexperience: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single job experience, or apperr.NotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (ExperienceDTO, error) {
	e, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return ExperienceDTO{}, fmt.Errorf("jobexperience: finding: %w", err)
	}
	if e == nil {
		return ExperienceDTO{}, apperr.NotFound("job experience not found")
	}
	return toDTO(*e), nil
}

// Create persists a new job experience.
func (s *Service) Create(ctx context.Context, req CreateExperienceRequest) (ExperienceDTO, error) {
	if err := s.checkPersonExists(ctx, req.PersonID); err != nil {
		return ExperienceDTO{}, err
	}
	if err := s.checkResumeExists(ctx, req.ResumeDocumentID); err != nil {
		return ExperienceDTO{}, err
	}
	start := req.StartDate.Time()
	end := endDateTime(req.EndDate)
	if err := validateDates(start, end); err != nil {
		return ExperienceDTO{}, err
	}

	e := Experience{
		ID: uuid.New(), PersonID: req.PersonID, Company: req.Company, Title: req.Title,
		PositionLevel: req.PositionLevel, Salary: req.Salary, ContractType: contractTypeOrDefault(req.ContractType),
		USDAmount: req.USDAmount, ExchangeRate: req.ExchangeRate, StartDate: start, EndDate: end,
		Notes: req.Notes, ResumeDocumentID: req.ResumeDocumentID,
	}
	if err := s.repo.Create(ctx, e); err != nil {
		return ExperienceDTO{}, err // already an *apperr.Error (FK violation) or a wrapped DB error
	}
	// Re-read rather than mapping `e` directly: CreatedAt/UpdatedAt are DB-generated DEFAULT now() —
	// same class of bug found systemically across this codebase (Hermes's HTTP sweep, msg 83 F-3).
	persisted, err := s.repo.FindByID(ctx, e.ID)
	if err != nil {
		return ExperienceDTO{}, fmt.Errorf("jobexperience: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// Update replaces a job experience's editable fields — full replace, PersonID is not re-settable
// (mirrors clients.UpdateClientRequest not carrying an owner change either). Returns
// apperr.NotFound if it doesn't exist.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateExperienceRequest) (ExperienceDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return ExperienceDTO{}, fmt.Errorf("jobexperience: finding: %w", err)
	}
	if existing == nil {
		return ExperienceDTO{}, apperr.NotFound("job experience not found")
	}
	if err := s.checkResumeExists(ctx, req.ResumeDocumentID); err != nil {
		return ExperienceDTO{}, err
	}
	start := req.StartDate.Time()
	end := endDateTime(req.EndDate)
	if err := validateDates(start, end); err != nil {
		return ExperienceDTO{}, err
	}

	updated := Experience{
		ID: id, PersonID: existing.PersonID, Company: req.Company, Title: req.Title,
		PositionLevel: req.PositionLevel, Salary: req.Salary, ContractType: contractTypeOrDefault(req.ContractType),
		USDAmount: req.USDAmount, ExchangeRate: req.ExchangeRate, StartDate: start, EndDate: end,
		Notes: req.Notes, ResumeDocumentID: req.ResumeDocumentID, CreatedAt: existing.CreatedAt,
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return ExperienceDTO{}, err
	}
	persisted, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return ExperienceDTO{}, fmt.Errorf("jobexperience: re-reading after update: %w", err)
	}
	return toDTO(*persisted), nil
}

// Delete removes a job experience outright, or apperr.NotFound if it doesn't exist.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("jobexperience: finding: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("job experience not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("jobexperience: deleting: %w", err)
	}
	return nil
}
