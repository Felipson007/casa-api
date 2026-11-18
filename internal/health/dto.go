package health

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// MarkerCatalogDTO is the JSON shape returned to clients for GET /api/health/markers.
type MarkerCatalogDTO struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Unit         *string   `json:"unit,omitempty"`
	ReferenceMin *string   `json:"referenceMin,omitempty"`
	ReferenceMax *string   `json:"referenceMax,omitempty"`
	RelatedOrgan *string   `json:"relatedOrgan,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// CreateMarkerCatalogRequest is the JSON body accepted by POST /api/health/markers.
type CreateMarkerCatalogRequest struct {
	Name         string  `json:"name" validate:"required"`
	Unit         *string `json:"unit"`
	ReferenceMin *string `json:"referenceMin" validate:"omitempty,numeric"`
	ReferenceMax *string `json:"referenceMax" validate:"omitempty,numeric"`
	RelatedOrgan *string `json:"relatedOrgan"`
}

// UpdateMarkerCatalogRequest is the JSON body accepted by PUT /api/health/markers/{id}.
type UpdateMarkerCatalogRequest struct {
	Name         string  `json:"name" validate:"required"`
	Unit         *string `json:"unit"`
	ReferenceMin *string `json:"referenceMin" validate:"omitempty,numeric"`
	ReferenceMax *string `json:"referenceMax" validate:"omitempty,numeric"`
	RelatedOrgan *string `json:"relatedOrgan"`
}

// RecordDTO is the JSON shape returned to clients for GET /api/health/records. RecordDate is
// civildate.Date, not time.Time — see its doc comment.
type RecordDTO struct {
	ID               uuid.UUID         `json:"id"`
	PersonID         uuid.UUID         `json:"personId"`
	Type             RecordType        `json:"type"`
	RecordDate       civildate.Date    `json:"recordDate"`
	Status           Status            `json:"status"`
	Amount           *string           `json:"amount,omitempty"`
	Unit             *string           `json:"unit,omitempty"`
	MarkerCatalogID  *uuid.UUID        `json:"markerCatalogId,omitempty"`
	Organ            *string           `json:"organ,omitempty"`
	AssessmentStatus *AssessmentStatus `json:"assessmentStatus,omitempty"`
	DocumentID       *uuid.UUID        `json:"documentId,omitempty"`
	Notes            *string           `json:"notes,omitempty"`
	CreatedAt        time.Time         `json:"createdAt"`
}

// CreateRecordRequest is the JSON body accepted by POST /api/health/records. Status always
// starts DONE — mirrors the DB column default.
type CreateRecordRequest struct {
	PersonID         uuid.UUID         `json:"personId" validate:"required"`
	Type             RecordType        `json:"type" validate:"required,oneof=WEIGHT HEIGHT BLOOD_PRESSURE BODY_FAT_PCT MUSCLE_MASS WAIST_CIRC HIP_CIRC VISCERAL_FAT BMR LAB_RESULT ORGAN_ASSESSMENT VACCINE CHECKUP MEDICAL_CONDITION MEDICATION BLOOD_TYPE ALLERGY HABIT FAMILY_HISTORY NOTE"`
	RecordDate       civildate.Date    `json:"recordDate" validate:"required"`
	Amount           *string           `json:"amount" validate:"omitempty,numeric"`
	Unit             *string           `json:"unit"`
	MarkerCatalogID  *uuid.UUID        `json:"markerCatalogId"`
	Organ            *string           `json:"organ"`
	AssessmentStatus *AssessmentStatus `json:"assessmentStatus" validate:"omitempty,oneof=NORMAL ATTENTION ALTERED"`
	DocumentID       *uuid.UUID        `json:"documentId"`
	Notes            *string           `json:"notes"`
}

// HealthProfileDTO is the aggregated, per-person health profile — mirrors the old Java
// RegistroSaudeService.perfilResumo (BMI/body-composition/organ-status rollup). It is DERIVED
// from the person's latest WEIGHT/HEIGHT/BODY_FAT_PCT/MUSCLE_MASS/WAIST_CIRC/HIP_CIRC/VISCERAL_FAT/
// BMR records (newest by RecordDate), not stored as its own table — the record CRUD is real,
// this summary view is computed on the fly from it. Mirrors what the old Spring /api/saude/pessoas
// /{id}/perfil returned; the frontend's usePerfilSaude hook was calling that legacy path and 404'ing.
type HealthProfileDTO struct {
	ID            uuid.UUID       `json:"id"`
	PersonID      uuid.UUID       `json:"personId"`
	WeightKg      *string         `json:"weightKg,omitempty"`      // latest WEIGHT record, numeric(10,2)
	HeightCm      *string         `json:"heightCm,omitempty"`      // latest HEIGHT record
	BMI           *string         `json:"bmi,omitempty"`           // derived: weight / (height_m^2)
	BodyFatPct    *string         `json:"bodyFatPct,omitempty"`    // latest BODY_FAT_PCT record
	MuscleMassKg  *string         `json:"muscleMassKg,omitempty"`  // latest MUSCLE_MASS record
	WaistCircCm   *string         `json:"waistCircCm,omitempty"`   // latest WAIST_CIRC record
	HipCircCm     *string         `json:"hipCircCm,omitempty"`     // latest HIP_CIRC record
	VisceralFat   *string         `json:"visceralFat,omitempty"`   // latest VISCERAL_FAT record
	BMR           *string         `json:"bmr,omitempty"`           // latest BMR record
	LastCheckedAt *civildate.Date `json:"lastCheckedAt,omitempty"` // newest record date driving the above
}

// UpdateRecordRequest is the JSON body accepted by PUT /api/health/records/{id}. Status is
// settable here — e.g. transitioning PENDING -> DONE/CANCELLED once results come in.
type UpdateRecordRequest struct {
	Type             RecordType        `json:"type" validate:"required,oneof=WEIGHT HEIGHT BLOOD_PRESSURE BODY_FAT_PCT MUSCLE_MASS WAIST_CIRC HIP_CIRC VISCERAL_FAT BMR LAB_RESULT ORGAN_ASSESSMENT VACCINE CHECKUP MEDICAL_CONDITION MEDICATION BLOOD_TYPE ALLERGY HABIT FAMILY_HISTORY NOTE"`
	RecordDate       civildate.Date    `json:"recordDate" validate:"required"`
	Status           Status            `json:"status" validate:"required,oneof=DONE PENDING CANCELLED"`
	Amount           *string           `json:"amount" validate:"omitempty,numeric"`
	Unit             *string           `json:"unit"`
	MarkerCatalogID  *uuid.UUID        `json:"markerCatalogId"`
	Organ            *string           `json:"organ"`
	AssessmentStatus *AssessmentStatus `json:"assessmentStatus" validate:"omitempty,oneof=NORMAL ATTENTION ALTERED"`
	DocumentID       *uuid.UUID        `json:"documentId"`
	Notes            *string           `json:"notes"`
}
