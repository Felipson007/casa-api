// Package health is the personal-health module (saude in the old app) — owned by Claude per
// decisions/DECISIONS.md D-005. Covers two tightly-coupled tables in one module (same idiom as
// loans covering Loan+Installment): MarkerCatalog (a lookup table of exam markers, e.g.
// "Glucose", "Cholesterol") and Record (an actual health record/exam result, optionally pointing
// at a marker and/or a document). Built after documents — a document with origin=LAB_RESULT is
// expected to be an exam that becomes a health Record (per Hermes, who confirmed this chain
// reading the old Java source).
package health

import (
	"time"

	"github.com/google/uuid"
)

// Status mirrors the health_records.status CHECK constraint.
type Status string

const (
	StatusDone      Status = "DONE"
	StatusPending   Status = "PENDING"
	StatusCancelled Status = "CANCELLED"
)

// RecordType mirrors the old Java TipoRegistroSaude enum, translated to English per the naming
// convention — confirmed with Hermes, who read the old codebase directly, since health_records.type
// has no CHECK constraint in the schema (unlike Status). Validated as a closed set at the Go layer
// (dto.go's "oneof" tag), same idiom as documents.Origin.
type RecordType string

const (
	RecordTypeWeight             RecordType = "WEIGHT"            // PESO
	RecordTypeHeight             RecordType = "HEIGHT"            // ALTURA
	RecordTypeBloodPressure      RecordType = "BLOOD_PRESSURE"    // PRESSAO_ARTERIAL
	RecordTypeBodyFatPercentage  RecordType = "BODY_FAT_PCT"      // PERCENTUAL_GORDURA
	RecordTypeMuscleMass         RecordType = "MUSCLE_MASS"       // MASSA_MUSCULAR
	RecordTypeWaistCircumference RecordType = "WAIST_CIRC"        // CIRCUNFERENCIA_CINTURA
	RecordTypeHipCircumference   RecordType = "HIP_CIRC"          // CIRCUNFERENCIA_QUADRIL
	RecordTypeVisceralFat        RecordType = "VISCERAL_FAT"      // GORDURA_VISCERAL
	RecordTypeBasalMetabolicRate RecordType = "BMR"               // TAXA_METABOLICA_BASAL
	RecordTypeLabResult          RecordType = "LAB_RESULT"        // EXAME_LABORATORIAL — chains from documents.OriginLabResult
	RecordTypeOrganAssessment    RecordType = "ORGAN_ASSESSMENT"  // AVALIACAO_ORGAO
	RecordTypeVaccine            RecordType = "VACCINE"           // VACINA
	RecordTypeCheckup            RecordType = "CHECKUP"           // CHECKUP
	RecordTypeMedicalCondition   RecordType = "MEDICAL_CONDITION" // CONDICAO_MEDICA
	RecordTypeMedication         RecordType = "MEDICATION"        // MEDICAMENTO
	RecordTypeBloodType          RecordType = "BLOOD_TYPE"        // TIPO_SANGUINEO
	RecordTypeAllergy            RecordType = "ALLERGY"           // ALERGIA
	RecordTypeHabit              RecordType = "HABIT"             // HABITO
	RecordTypeFamilyHistory      RecordType = "FAMILY_HISTORY"    // HISTORICO_FAMILIAR
	RecordTypeNote               RecordType = "NOTE"              // NOTA
)

// AssessmentStatus mirrors the old Java StatusAvaliacaoSaude enum, translated to English. Same
// no-CHECK-in-schema situation as RecordType.
type AssessmentStatus string

const (
	AssessmentStatusNormal    AssessmentStatus = "NORMAL"    // NORMAL
	AssessmentStatusAttention AssessmentStatus = "ATTENTION" // ATENCAO
	AssessmentStatusAltered   AssessmentStatus = "ALTERED"   // ALTERADO
)

// MarkerCatalog mirrors the health_marker_catalog table
// (internal/migrations/000001_init_schema.up.sql) — a lookup/reference entity, similar shape to
// categories.
type MarkerCatalog struct {
	ID           uuid.UUID
	Name         string
	Unit         *string
	ReferenceMin *string // numeric(10,2) — kept as string to avoid float rounding
	ReferenceMax *string
	RelatedOrgan *string
	CreatedAt    time.Time
}

// Record mirrors the health_records table. PersonID is NOT NULL (unlike most other modules'
// person links) — a health record always belongs to a specific person. MarkerCatalogID and
// DocumentID are nullable FKs with ON DELETE SET NULL.
type Record struct {
	ID               uuid.UUID
	PersonID         uuid.UUID
	Type             RecordType
	RecordDate       time.Time
	Status           Status
	Amount           *string // numeric(10,2)
	Unit             *string
	MarkerCatalogID  *uuid.UUID
	Organ            *string
	AssessmentStatus *AssessmentStatus
	DocumentID       *uuid.UUID
	Notes            *string
	CreatedAt        time.Time
}
