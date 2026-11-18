package calendar

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// EventDTO is the JSON shape returned to clients. StartDate/EndDate are bare calendar dates
// (civildate), never instants; StartTime/EndTime are "HH:MM" and only present for timed events.
type EventDTO struct {
	ID          uuid.UUID       `json:"id"`
	Title       string          `json:"title"`
	Description *string         `json:"description,omitempty"`
	Location    *string         `json:"location,omitempty"`
	AllDay      bool            `json:"allDay"`
	StartDate   civildate.Date  `json:"startDate"`
	EndDate     *civildate.Date `json:"endDate,omitempty"`
	StartTime   *string         `json:"startTime,omitempty"`
	EndTime     *string         `json:"endTime,omitempty"`
	Source      Source          `json:"source"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

// CreateEventRequest is the body for POST /api/calendar/events. StartDate is a pointer so a missing
// date fails validation (a zero civildate.Date would otherwise slip through `required`).
type CreateEventRequest struct {
	Title       string          `json:"title" validate:"required,max=200"`
	Description *string         `json:"description" validate:"omitempty,max=2000"`
	Location    *string         `json:"location" validate:"omitempty,max=200"`
	AllDay      *bool           `json:"allDay"`
	StartDate   *civildate.Date `json:"startDate" validate:"required"`
	EndDate     *civildate.Date `json:"endDate"`
	StartTime   *string         `json:"startTime" validate:"omitempty,datetime=15:04"`
	EndTime     *string         `json:"endTime" validate:"omitempty,datetime=15:04"`
}

// UpdateEventRequest is the body for PUT /api/calendar/events/{id} — full replace, same convention
// as goals.UpdateGoalRequest.
type UpdateEventRequest = CreateEventRequest
