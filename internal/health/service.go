package health

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom. Covers both MarkerCatalog and Record, same
// as internal/loans covers Loan+Installment.
type Repository interface {
	ListMarkers(ctx context.Context) ([]MarkerCatalog, error)
	FindMarkerByID(ctx context.Context, id uuid.UUID) (*MarkerCatalog, error)
	CreateMarker(ctx context.Context, m MarkerCatalog) error
	UpdateMarker(ctx context.Context, m MarkerCatalog) error
	DeleteMarker(ctx context.Context, id uuid.UUID) error

	ListRecords(ctx context.Context, personID, markerCatalogID *uuid.UUID) ([]Record, error)
	FindRecordByID(ctx context.Context, id uuid.UUID) (*Record, error)
	CreateRecord(ctx context.Context, r Record) error
	UpdateRecord(ctx context.Context, r Record) error
	DeleteRecord(ctx context.Context, id uuid.UUID) error
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13).
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// ListMarkers returns every marker catalog entry.
func (s *Service) ListMarkers(ctx context.Context) ([]MarkerCatalogDTO, error) {
	list, err := s.repo.ListMarkers(ctx)
	if err != nil {
		return nil, fmt.Errorf("health: listing markers: %w", err)
	}
	return toMarkerDTOs(list), nil
}

// GetMarker returns a single marker catalog entry, or apperr.NotFound if it doesn't exist.
func (s *Service) GetMarker(ctx context.Context, id uuid.UUID) (MarkerCatalogDTO, error) {
	m, err := s.repo.FindMarkerByID(ctx, id)
	if err != nil {
		return MarkerCatalogDTO{}, fmt.Errorf("health: looking up marker: %w", err)
	}
	if m == nil {
		return MarkerCatalogDTO{}, apperr.NotFound("marker not found")
	}
	return toMarkerDTO(*m), nil
}

// CreateMarker persists a new marker catalog entry.
func (s *Service) CreateMarker(ctx context.Context, req CreateMarkerCatalogRequest) (MarkerCatalogDTO, error) {
	m := MarkerCatalog{
		ID:           uuid.New(),
		Name:         req.Name,
		Unit:         req.Unit,
		ReferenceMin: req.ReferenceMin,
		ReferenceMax: req.ReferenceMax,
		RelatedOrgan: req.RelatedOrgan,
	}
	if err := s.repo.CreateMarker(ctx, m); err != nil {
		return MarkerCatalogDTO{}, fmt.Errorf("health: creating marker: %w", err)
	}
	// Re-read rather than mapping the in-memory `m` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above (found systemically across every Create in this codebase by
	// Hermes's HTTP endpoint sweep on loans, msg 83 F-3).
	persisted, err := s.repo.FindMarkerByID(ctx, m.ID)
	if err != nil {
		return MarkerCatalogDTO{}, fmt.Errorf("health: re-reading marker after create: %w", err)
	}
	return toMarkerDTO(*persisted), nil
}

// UpdateMarker replaces a marker catalog entry's fields, or returns apperr.NotFound if it doesn't
// exist.
func (s *Service) UpdateMarker(ctx context.Context, id uuid.UUID, req UpdateMarkerCatalogRequest) (MarkerCatalogDTO, error) {
	existing, err := s.repo.FindMarkerByID(ctx, id)
	if err != nil {
		return MarkerCatalogDTO{}, fmt.Errorf("health: looking up marker for update: %w", err)
	}
	if existing == nil {
		return MarkerCatalogDTO{}, apperr.NotFound("marker not found")
	}

	updated := MarkerCatalog{
		ID:           id,
		Name:         req.Name,
		Unit:         req.Unit,
		ReferenceMin: req.ReferenceMin,
		ReferenceMax: req.ReferenceMax,
		RelatedOrgan: req.RelatedOrgan,
		CreatedAt:    existing.CreatedAt,
	}
	if err := s.repo.UpdateMarker(ctx, updated); err != nil {
		return MarkerCatalogDTO{}, fmt.Errorf("health: updating marker: %w", err)
	}
	// Re-read rather than mapping `updated` directly: ReferenceMin/ReferenceMax are echoed back at
	// whatever scale the caller sent them, not the DB's canonical numeric form — same class of bug
	// as F-3 (Hermes's HTTP endpoint sweep, msg 83), just on Update instead of Create.
	persisted, err := s.repo.FindMarkerByID(ctx, id)
	if err != nil {
		return MarkerCatalogDTO{}, fmt.Errorf("health: re-reading marker after update: %w", err)
	}
	return toMarkerDTO(*persisted), nil
}

// DeleteMarker removes a marker catalog entry by ID, or returns apperr.NotFound if it doesn't
// exist. health_records.marker_catalog_id is ON DELETE SET NULL — deleting a marker just
// disassociates existing records from it, it doesn't fail or cascade-delete them.
func (s *Service) DeleteMarker(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindMarkerByID(ctx, id)
	if err != nil {
		return fmt.Errorf("health: looking up marker for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("marker not found")
	}
	if err := s.repo.DeleteMarker(ctx, id); err != nil {
		return fmt.Errorf("health: deleting marker: %w", err)
	}
	return nil
}

// ListRecords returns health records, optionally filtered by person and/or marker catalog entry.
func (s *Service) ListRecords(ctx context.Context, personID, markerCatalogID *uuid.UUID) ([]RecordDTO, error) {
	list, err := s.repo.ListRecords(ctx, personID, markerCatalogID)
	if err != nil {
		return nil, fmt.Errorf("health: listing records: %w", err)
	}
	return toRecordDTOs(list), nil
}

// GetRecord returns a single health record, or apperr.NotFound if it doesn't exist.
func (s *Service) GetRecord(ctx context.Context, id uuid.UUID) (RecordDTO, error) {
	r, err := s.repo.FindRecordByID(ctx, id)
	if err != nil {
		return RecordDTO{}, fmt.Errorf("health: looking up record: %w", err)
	}
	if r == nil {
		return RecordDTO{}, apperr.NotFound("health record not found")
	}
	return toRecordDTO(*r), nil
}

// CreateRecord persists a new health record. Status always starts DONE.
func (s *Service) CreateRecord(ctx context.Context, req CreateRecordRequest) (RecordDTO, error) {
	r := Record{
		ID:               uuid.New(),
		PersonID:         req.PersonID,
		Type:             req.Type,
		RecordDate:       req.RecordDate.Time(),
		Status:           StatusDone,
		Amount:           req.Amount,
		Unit:             req.Unit,
		MarkerCatalogID:  req.MarkerCatalogID,
		Organ:            req.Organ,
		AssessmentStatus: req.AssessmentStatus,
		DocumentID:       req.DocumentID,
		Notes:            req.Notes,
	}
	if err := s.repo.CreateRecord(ctx, r); err != nil {
		return RecordDTO{}, err // already an *apperr.Error (FK violation) or a wrapped DB error
	}
	// Re-read rather than mapping the in-memory `r` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above (found systemically across every Create in this codebase by
	// Hermes's HTTP endpoint sweep on loans, msg 83 F-3).
	persisted, err := s.repo.FindRecordByID(ctx, r.ID)
	if err != nil {
		return RecordDTO{}, fmt.Errorf("health: re-reading record after create: %w", err)
	}
	return toRecordDTO(*persisted), nil
}

// UpdateRecord replaces a health record's editable fields (including Status), or returns
// apperr.NotFound if it doesn't exist. PersonID is not re-settable — a record doesn't change
// whose it is after creation.
func (s *Service) UpdateRecord(ctx context.Context, id uuid.UUID, req UpdateRecordRequest) (RecordDTO, error) {
	existing, err := s.repo.FindRecordByID(ctx, id)
	if err != nil {
		return RecordDTO{}, fmt.Errorf("health: looking up record for update: %w", err)
	}
	if existing == nil {
		return RecordDTO{}, apperr.NotFound("health record not found")
	}

	updated := Record{
		ID:               id,
		PersonID:         existing.PersonID,
		Type:             req.Type,
		RecordDate:       req.RecordDate.Time(),
		Status:           req.Status,
		Amount:           req.Amount,
		Unit:             req.Unit,
		MarkerCatalogID:  req.MarkerCatalogID,
		Organ:            req.Organ,
		AssessmentStatus: req.AssessmentStatus,
		DocumentID:       req.DocumentID,
		Notes:            req.Notes,
		CreatedAt:        existing.CreatedAt,
	}
	if err := s.repo.UpdateRecord(ctx, updated); err != nil {
		return RecordDTO{}, err
	}
	// Re-read rather than mapping `updated` directly: Amount is echoed back at whatever scale the
	// caller sent it, not the DB's canonical numeric form — same class of bug as F-3 (Hermes's
	// HTTP endpoint sweep, msg 83), just on Update instead of Create.
	persisted, err := s.repo.FindRecordByID(ctx, id)
	if err != nil {
		return RecordDTO{}, fmt.Errorf("health: re-reading record after update: %w", err)
	}
	return toRecordDTO(*persisted), nil
}

// typeAliases maps the legacy Portuguese record-type strings that the Java-era seed stored in
// health_records.type (e.g. "PESO", "ALTURA") to the Go RecordType constants ("WEIGHT", "HEIGHT").
// The column has NO CHECK constraint (see domain.go), so legacy seeds can land with PT values
// while new writes use the EN enum — normalize both to the canonical EN constant so downstream
// code (PerfilSaude's latest[RecordTypeWeight] lookup, the /detail aggregate) always matches.
// Bidirectional: unknown values pass through unchanged (forward-compat with future type additions).
var typeAliases = map[string]RecordType{
	"PESO":                   RecordTypeWeight,
	"ALTURA":                 RecordTypeHeight,
	"PRESSAO_ARTERIAL":       RecordTypeBloodPressure,
	"PERCENTUAL_GORDURA":     RecordTypeBodyFatPercentage,
	"MASSA_MUSCULAR":         RecordTypeMuscleMass,
	"CIRCUNFERENCIA_CINTURA": RecordTypeWaistCircumference,
	"CIRCUNFERENCIA_QUADRIL": RecordTypeHipCircumference,
	"GORDURA_VISCERAL":       RecordTypeVisceralFat,
	"TAXA_METABOLICA_BASAL":  RecordTypeBasalMetabolicRate,
	"EXAME_LABORATORIAL":     RecordTypeLabResult,
	"AVALIACAO_ORGAO":        RecordTypeOrganAssessment,
	"VACINA":                 RecordTypeVaccine,
	"CHECKUP":                RecordTypeCheckup,
	"CONDICAO_MEDICA":        RecordTypeMedicalCondition,
	"MEDICAMENTO":            RecordTypeMedication,
	"TIPO_SANGUINEO":         RecordTypeBloodType,
	"ALERGIA":                RecordTypeAllergy,
	"HABITO":                 RecordTypeHabit,
	"HISTORICO_FAMILIAR":     RecordTypeFamilyHistory,
	"NOTA":                   RecordTypeNote,
}

// normalizeRecordType canonicalises a raw record.Type to its English enum constant.
// Known PT aliases are mapped; EN values and unknowns pass through.
func normalizeRecordType(t RecordType) RecordType {
	if aliased, ok := typeAliases[string(t)]; ok {
		return aliased
	}
	return t
}

// PerfilSaude derives the per-person health profile from the person's latest records of each
// measurement type (WEIGHT/HEIGHT/BODY_FAT_PCT/MUSCLE_MASS/WAIST_CIRC/HIP_CIRC/VISCERAL_FAT/BMR)
// — mirrors RegistroSaudeService.perfilResumo. One ListRecords call (single query, all records
// for the person) + in-memory rollup: O(1) round trips. BMI is derived as weight/(height_m^2)
// to match the old Java's IMC calculation (confirmed against dashboard/service.go's
// calculatePortfolioScore doc — same numeric(10,2) handling). Returns apperr.NotFound when the
// person has no records at all (distinct from "person doesn't exist", which the people module owns).
func (s *Service) PerfilSaude(ctx context.Context, personID uuid.UUID) (HealthProfileDTO, error) {
	records, err := s.repo.ListRecords(ctx, &personID, nil)
	if err != nil {
		return HealthProfileDTO{}, fmt.Errorf("health: profiling: listing records: %w", err)
	}
	latest := map[RecordType]Record{}
	var newest *Record
	for _, r := range records {
		rt := normalizeRecordType(r.Type) // normalize legacy PT ("PESO") -> EN ("WEIGHT") before lookup
		if prev, ok := latest[rt]; !ok || r.RecordDate.After(prev.RecordDate) {
			latest[rt] = r
		}
		if newest == nil || r.RecordDate.After(newest.RecordDate) {
			newest = &r
		}
	}
	profile := HealthProfileDTO{ID: uuid.New(), PersonID: personID}
	if w := latest[RecordTypeWeight]; w.Amount != nil {
		profile.WeightKg = w.Amount
	}
	if h := latest[RecordTypeHeight]; h.Amount != nil {
		profile.HeightCm = h.Amount
	}
	profile.BMI = deriveBMI(profile.WeightKg, profile.HeightCm)
	if b := latest[RecordTypeBodyFatPercentage]; b.Amount != nil {
		profile.BodyFatPct = b.Amount
	}
	if m := latest[RecordTypeMuscleMass]; m.Amount != nil {
		profile.MuscleMassKg = m.Amount
	}
	if wc := latest[RecordTypeWaistCircumference]; wc.Amount != nil {
		profile.WaistCircCm = wc.Amount
	}
	if hc := latest[RecordTypeHipCircumference]; hc.Amount != nil {
		profile.HipCircCm = hc.Amount
	}
	if vf := latest[RecordTypeVisceralFat]; vf.Amount != nil {
		profile.VisceralFat = vf.Amount
	}
	if bmr := latest[RecordTypeBasalMetabolicRate]; bmr.Amount != nil {
		profile.BMR = bmr.Amount
	}
	if newest != nil {
		profile.LastCheckedAt = civildate.OfPtr(&newest.RecordDate)
	}
	return profile, nil
}

// deriveBMI computes weight_kg / (height_m^2), returning nil when either is absent or
// non-numeric (same guard the old Java PessoaSaudeService.imc used).
func deriveBMI(weightKg, heightCm *string) *string {
	if weightKg == nil || heightCm == nil || *weightKg == "" || *heightCm == "" {
		return nil
	}
	w, err := strconv.ParseFloat(*weightKg, 64)
	if err != nil {
		return nil
	}
	h, err := strconv.ParseFloat(*heightCm, 64)
	if err != nil {
		return nil
	}
	if h <= 0 {
		return nil
	}
	bmi := w / ((h / 100) * (h / 100))
	s := strconv.FormatFloat(bmi, 'f', 1, 64)
	return &s
}

// DeleteRecord removes a health record by ID, or returns apperr.NotFound if it doesn't exist.
func (s *Service) DeleteRecord(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindRecordByID(ctx, id)
	if err != nil {
		return fmt.Errorf("health: looking up record for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("health record not found")
	}
	if err := s.repo.DeleteRecord(ctx, id); err != nil {
		return fmt.Errorf("health: deleting record: %w", err)
	}
	return nil
}
