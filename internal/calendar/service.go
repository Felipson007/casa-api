package calendar

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// Repository is defined at the point of consumption (Go idiom). The postgres subpackage implements
// it; nothing here imports postgres.
type Repository interface {
	// ListRange returns live events overlapping [from, to] inclusive.
	ListRange(ctx context.Context, from, to time.Time) ([]Event, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Event, error)
	Create(ctx context.Context, e Event) error
	Update(ctx context.Context, e Event) error
	// Delete soft-deletes: sets deleted_at, leaving the row for a future sync to propagate the
	// removal instead of silently dropping the external mapping.
	Delete(ctx context.Context, id uuid.UUID) error

	// UpsertDerived materializes an Atlas-owned (read-only) event, keyed by its deterministic id.
	UpsertDerived(ctx context.Context, e Event) error
	// SoftDeleteReadOnlyNotIn soft-deletes read-only rows whose id is not in keep (a derived event
	// whose source disappeared).
	SoftDeleteReadOnlyNotIn(ctx context.Context, keep []uuid.UUID) error
}

// DerivedEvent is an Atlas-owned calendar entry projected from another module (loan instalment,
// birthday, document expiration). Key is a stable identifier (e.g. "installment-<loan>-<date>").
type DerivedEvent struct {
	Key         string
	Title       string
	Description *string
	StartDate   time.Time
}

// ReplaceDerived materializes the current set of derived events and prunes the ones that no longer
// exist. Rows are keyed by a deterministic UUID so a re-run updates rather than duplicates.
func (s *Service) ReplaceDerived(ctx context.Context, items []DerivedEvent) error {
	keep := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(it.Key))
		keep = append(keep, id)
		e := Event{
			ID:          id,
			Title:       it.Title,
			Description: it.Description,
			AllDay:      true,
			StartDate:   dayStart(it.StartDate),
			Source:      SourceManual,
			ReadOnly:    true,
		}
		if err := s.repo.UpsertDerived(ctx, e); err != nil {
			return fmt.Errorf("calendar: upserting derived event: %w", err)
		}
	}
	if err := s.repo.SoftDeleteReadOnlyNotIn(ctx, keep); err != nil {
		return fmt.Errorf("calendar: pruning derived events: %w", err)
	}
	return nil
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// ListRange returns every event overlapping [from, to], ordered by start date then title.
func (s *Service) ListRange(ctx context.Context, from, to time.Time) ([]EventDTO, error) {
	if to.Before(from) {
		return nil, apperr.Validation("to must not be before from", "to")
	}
	list, err := s.repo.ListRange(ctx, dayStart(from), dayStart(to))
	if err != nil {
		return nil, fmt.Errorf("calendar: listing range: %w", err)
	}
	out := make([]EventDTO, len(list))
	for i, e := range list {
		out[i] = toDTO(e)
	}
	return out, nil
}

func (s *Service) Create(ctx context.Context, req CreateEventRequest) (EventDTO, error) {
	e, err := s.eventFromRequest(req)
	if err != nil {
		return EventDTO{}, err
	}
	e.ID = uuid.New()
	e.Source = SourceManual
	if err := s.repo.Create(ctx, e); err != nil {
		return EventDTO{}, err
	}
	persisted, err := s.repo.FindByID(ctx, e.ID)
	if err != nil {
		return EventDTO{}, fmt.Errorf("calendar: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, req CreateEventRequest) (EventDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return EventDTO{}, fmt.Errorf("calendar: looking up for update: %w", err)
	}
	if existing == nil {
		return EventDTO{}, apperr.NotFound("event not found")
	}
	e, err := s.eventFromRequest(req)
	if err != nil {
		return EventDTO{}, err
	}
	e.ID = id
	// A synced event keeps its source and external mapping on edit; a manual event stays manual.
	// ExternalSyncedAt is deliberately NOT copied: the repository's API-update path leaves the
	// column untouched, so an edit keeps the row "locally dirty" for the next sync to push.
	e.Source = existing.Source
	e.ExternalProvider = existing.ExternalProvider
	e.ExternalID = existing.ExternalID
	e.ExternalCalendarID = existing.ExternalCalendarID
	e.ExternalHref = existing.ExternalHref
	e.ETag = existing.ETag

	if err := s.repo.Update(ctx, e); err != nil {
		return EventDTO{}, err
	}
	persisted, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return EventDTO{}, fmt.Errorf("calendar: re-reading after update: %w", err)
	}
	return toDTO(*persisted), nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("calendar: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("event not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("calendar: deleting: %w", err)
	}
	return nil
}

// eventFromRequest validates and normalizes the body. allDay defaults to true; a timed event
// requires a start time and, if it also has an end time, one strictly after the start.
func (s *Service) eventFromRequest(req CreateEventRequest) (Event, error) {
	allDay := true
	if req.AllDay != nil {
		allDay = *req.AllDay
	}

	start := req.StartDate.Time()
	e := Event{
		Title:       req.Title,
		Description: req.Description,
		Location:    req.Location,
		AllDay:      allDay,
		StartDate:   dayStart(start),
	}

	if req.EndDate != nil {
		end := dayStart(req.EndDate.Time())
		if end.Before(dayStart(start)) {
			return Event{}, apperr.Validation("endDate must not be before startDate", "endDate")
		}
		e.EndDate = &end
	}

	if allDay {
		if req.StartTime != nil || req.EndTime != nil {
			return Event{}, apperr.Validation("all-day events must not carry a time", "startTime")
		}
		return e, nil
	}

	if req.StartTime == nil {
		return Event{}, apperr.Validation("startTime is required for a timed event", "startTime")
	}
	e.StartTime = req.StartTime
	e.EndTime = req.EndTime
	if req.EndTime != nil {
		startAt, err := time.Parse("15:04", *req.StartTime)
		if err != nil {
			return Event{}, apperr.Validation("startTime must be HH:MM", "startTime")
		}
		endAt, err := time.Parse("15:04", *req.EndTime)
		if err != nil {
			return Event{}, apperr.Validation("endTime must be HH:MM", "endTime")
		}
		if !endAt.After(startAt) {
			return Event{}, apperr.Validation("endTime must be after startTime", "endTime")
		}
	}
	return e, nil
}

func toDTO(e Event) EventDTO {
	return EventDTO{
		ID:          e.ID,
		Title:       e.Title,
		Description: e.Description,
		Location:    e.Location,
		AllDay:      e.AllDay,
		StartDate:   civildate.Of(e.StartDate),
		EndDate:     civildate.OfPtr(e.EndDate),
		StartTime:   e.StartTime,
		EndTime:     e.EndTime,
		Source:      e.Source,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
