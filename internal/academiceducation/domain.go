// Package academiceducation is structured education history (institution, course, level, period)
// for a person — the "Educação" screen's data source, mirroring internal/jobexperience's role for
// "Carreira". Replaces the old approach of deriving education facts from generic
// person_information key/value rows plus diploma documents: this module gives it a real table with
// typed fields instead of free text.
package academiceducation

import (
	"time"

	"github.com/google/uuid"
)

// Education mirrors the academic_educations table (migration
// 000028_academic_educations.up.sql). EndDate == nil means "cursando" — IsCurrent is derived at
// the DTO layer (mapper.go), never persisted. DiplomaDocumentID optionally links a filed
// diploma/certificate; it is never auto-extracted, purely a reference kept alongside the
// manually-entered structured data.
type Education struct {
	ID                uuid.UUID
	PersonID          uuid.UUID
	Institution       string
	Course            string
	EducationLevel    *string
	StartDate         time.Time
	EndDate           *time.Time
	Notes             *string
	DiplomaDocumentID *uuid.UUID
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
