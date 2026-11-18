package academiceducation

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// EducationDTO is the JSON shape returned to clients. IsCurrent is derived (EndDate == nil), never
// stored — see Education's doc comment.
type EducationDTO struct {
	ID                uuid.UUID       `json:"id"`
	PersonID          uuid.UUID       `json:"personId"`
	Institution       string          `json:"institution"`
	Course            string          `json:"course"`
	EducationLevel    *string         `json:"educationLevel,omitempty"`
	StartDate         civildate.Date  `json:"startDate"`
	EndDate           *civildate.Date `json:"endDate,omitempty"`
	IsCurrent         bool            `json:"isCurrent"`
	Notes             *string         `json:"notes,omitempty"`
	DiplomaDocumentID *uuid.UUID      `json:"diplomaDocumentId,omitempty"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

// CreateEducationRequest is the JSON body accepted by POST /api/academic-educations.
type CreateEducationRequest struct {
	PersonID          uuid.UUID       `json:"personId" validate:"required"`
	Institution       string          `json:"institution" validate:"required,max=200"`
	Course            string          `json:"course" validate:"required,max=200"`
	EducationLevel    *string         `json:"educationLevel" validate:"omitempty,max=200"`
	StartDate         civildate.Date  `json:"startDate" validate:"required"`
	EndDate           *civildate.Date `json:"endDate"`
	Notes             *string         `json:"notes"`
	DiplomaDocumentID *uuid.UUID      `json:"diplomaDocumentId"`
}

// UpdateEducationRequest is the JSON body accepted by PUT /api/academic-educations/{id} — full
// replace, same convention as jobexperience.UpdateExperienceRequest (PersonID is not re-settable).
type UpdateEducationRequest struct {
	Institution       string          `json:"institution" validate:"required,max=200"`
	Course            string          `json:"course" validate:"required,max=200"`
	EducationLevel    *string         `json:"educationLevel" validate:"omitempty,max=200"`
	StartDate         civildate.Date  `json:"startDate" validate:"required"`
	EndDate           *civildate.Date `json:"endDate"`
	Notes             *string         `json:"notes"`
	DiplomaDocumentID *uuid.UUID      `json:"diplomaDocumentId"`
}
