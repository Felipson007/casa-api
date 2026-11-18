package academiceducation

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

// isNotFound reports whether err is an apperr.Error of KindNotFound — same idiom as
// internal/jobexperience.
func isNotFound(err error) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Kind == apperr.KindNotFound
}

// endDateTime converts a request's optional *civildate.Date into the *time.Time the domain struct
// uses internally — nil-safe (nil EndDate means "cursando").
func endDateTime(d *civildate.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}

// validateDates rejects an EndDate before StartDate — mirrors the DB's own CHECK constraint
// (migration 000022), same pattern as internal/jobexperience.
func validateDates(start time.Time, end *time.Time) error {
	if end != nil && end.Before(start) {
		return apperr.Validation("endDate cannot be before startDate", "endDate")
	}
	return nil
}

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/jobexperience/service.go for the same idiom.
type Repository interface {
	List(ctx context.Context, personID uuid.UUID) ([]Education, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Education, error)
	Create(ctx context.Context, e Education) error
	Update(ctx context.Context, e Education) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// Service holds the business rules — mirrors internal/jobexperience.Service's shape exactly
// (people/documents are the concrete sibling services; Create/Update only ever call their
// existing Get methods to check a referenced person/diploma exists).
type Service struct {
	repo      Repository
	people    *people.Service
	documents *documents.Service
}

func NewService(repo Repository, peopleSvc *people.Service, documentsSvc *documents.Service) *Service {
	return &Service{repo: repo, people: peopleSvc, documents: documentsSvc}
}

func (s *Service) checkPersonExists(ctx context.Context, personID uuid.UUID) error {
	if _, err := s.people.GetPerson(ctx, personID); err != nil {
		if isNotFound(err) {
			return apperr.NotFound("person not found")
		}
		return fmt.Errorf("academiceducation: looking up person: %w", err)
	}
	return nil
}

func (s *Service) checkDiplomaExists(ctx context.Context, diplomaDocumentID *uuid.UUID) error {
	if diplomaDocumentID == nil {
		return nil
	}
	if _, err := s.documents.Get(ctx, *diplomaDocumentID); err != nil {
		if isNotFound(err) {
			return apperr.NotFound("diploma document not found")
		}
		return fmt.Errorf("academiceducation: looking up diploma document: %w", err)
	}
	return nil
}

// List returns a person's education history, most recent first (see postgres.Repository.List).
func (s *Service) List(ctx context.Context, personID uuid.UUID) ([]EducationDTO, error) {
	list, err := s.repo.List(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("academiceducation: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single education record, or apperr.NotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (EducationDTO, error) {
	e, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return EducationDTO{}, fmt.Errorf("academiceducation: finding: %w", err)
	}
	if e == nil {
		return EducationDTO{}, apperr.NotFound("education record not found")
	}
	return toDTO(*e), nil
}

// Create persists a new education record.
func (s *Service) Create(ctx context.Context, req CreateEducationRequest) (EducationDTO, error) {
	if err := s.checkPersonExists(ctx, req.PersonID); err != nil {
		return EducationDTO{}, err
	}
	if err := s.checkDiplomaExists(ctx, req.DiplomaDocumentID); err != nil {
		return EducationDTO{}, err
	}
	start := req.StartDate.Time()
	end := endDateTime(req.EndDate)
	if err := validateDates(start, end); err != nil {
		return EducationDTO{}, err
	}

	e := Education{
		ID: uuid.New(), PersonID: req.PersonID, Institution: req.Institution, Course: req.Course,
		EducationLevel: req.EducationLevel, StartDate: start, EndDate: end,
		Notes: req.Notes, DiplomaDocumentID: req.DiplomaDocumentID,
	}
	if err := s.repo.Create(ctx, e); err != nil {
		return EducationDTO{}, err
	}
	// Re-read rather than mapping `e` directly: CreatedAt/UpdatedAt are DB-generated DEFAULT now() —
	// same convention as internal/jobexperience.Service.Create.
	persisted, err := s.repo.FindByID(ctx, e.ID)
	if err != nil {
		return EducationDTO{}, fmt.Errorf("academiceducation: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// Update replaces an education record's editable fields — full replace, PersonID is not
// re-settable. Returns apperr.NotFound if it doesn't exist.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateEducationRequest) (EducationDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return EducationDTO{}, fmt.Errorf("academiceducation: finding: %w", err)
	}
	if existing == nil {
		return EducationDTO{}, apperr.NotFound("education record not found")
	}
	if err := s.checkDiplomaExists(ctx, req.DiplomaDocumentID); err != nil {
		return EducationDTO{}, err
	}
	start := req.StartDate.Time()
	end := endDateTime(req.EndDate)
	if err := validateDates(start, end); err != nil {
		return EducationDTO{}, err
	}

	updated := Education{
		ID: id, PersonID: existing.PersonID, Institution: req.Institution, Course: req.Course,
		EducationLevel: req.EducationLevel, StartDate: start, EndDate: end,
		Notes: req.Notes, DiplomaDocumentID: req.DiplomaDocumentID, CreatedAt: existing.CreatedAt,
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return EducationDTO{}, err
	}
	persisted, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return EducationDTO{}, fmt.Errorf("academiceducation: re-reading after update: %w", err)
	}
	return toDTO(*persisted), nil
}

// Delete removes an education record outright, or apperr.NotFound if it doesn't exist.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("academiceducation: finding: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("education record not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("academiceducation: deleting: %w", err)
	}
	return nil
}
