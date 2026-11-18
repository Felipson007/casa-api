package academiceducation

import "github.com/atlas/atlas-go/internal/platform/civildate"

func toDTO(e Education) EducationDTO {
	return EducationDTO{
		ID:                e.ID,
		PersonID:          e.PersonID,
		Institution:       e.Institution,
		Course:            e.Course,
		EducationLevel:    e.EducationLevel,
		StartDate:         civildate.Of(e.StartDate),
		EndDate:           civildate.OfPtr(e.EndDate),
		IsCurrent:         e.EndDate == nil,
		Notes:             e.Notes,
		DiplomaDocumentID: e.DiplomaDocumentID,
		CreatedAt:         e.CreatedAt,
		UpdatedAt:         e.UpdatedAt,
	}
}

func toDTOs(es []Education) []EducationDTO {
	dtos := make([]EducationDTO, len(es))
	for i, e := range es {
		dtos[i] = toDTO(e)
	}
	return dtos
}
