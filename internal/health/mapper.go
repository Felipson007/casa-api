package health

import (
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

func toMarkerDTO(m MarkerCatalog) MarkerCatalogDTO {
	return MarkerCatalogDTO{
		ID:           m.ID,
		Name:         m.Name,
		Unit:         m.Unit,
		ReferenceMin: m.ReferenceMin,
		ReferenceMax: m.ReferenceMax,
		RelatedOrgan: m.RelatedOrgan,
		CreatedAt:    m.CreatedAt,
	}
}

func toMarkerDTOs(ms []MarkerCatalog) []MarkerCatalogDTO {
	dtos := make([]MarkerCatalogDTO, len(ms))
	for i, m := range ms {
		dtos[i] = toMarkerDTO(m)
	}
	return dtos
}

// normalizeRecordType canonicalizes a raw record.Type to its English enum
// constant. The column has NO CHECK constraint (see domain.go), so legacy
// Java-era seeds land with PT strings ("PESO", "ALTURA") while new writes use
// EN ("WEIGHT", "HEIGHT"). Without this, the RecordDTO would emit "PESO" and
// the frontend's tiposDeRegistro mapper would fall back to "Nota" instead of
// "Peso". Known aliases are mapped; EN values and unknowns pass through.
func toRecordDTO(r Record) RecordDTO {
	return RecordDTO{
		ID:               r.ID,
		PersonID:         r.PersonID,
		Type:             normalizeRecordType(r.Type),
		RecordDate:       civildate.Of(r.RecordDate),
		Status:           r.Status,
		Amount:           r.Amount,
		Unit:             r.Unit,
		MarkerCatalogID:  r.MarkerCatalogID,
		Organ:            r.Organ,
		AssessmentStatus: r.AssessmentStatus,
		DocumentID:       r.DocumentID,
		Notes:            r.Notes,
		CreatedAt:        r.CreatedAt,
	}
}

func toRecordDTOs(rs []Record) []RecordDTO {
	dtos := make([]RecordDTO, len(rs))
	for i, r := range rs {
		dtos[i] = toRecordDTO(r)
	}
	return dtos
}
