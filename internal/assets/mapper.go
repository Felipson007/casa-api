package assets

import "github.com/atlas/atlas-go/internal/platform/civildate"

func toDTO(a Asset) AssetDTO {
	return AssetDTO{
		ID:                    a.ID,
		PersonID:              a.PersonID,
		Name:                  a.Name,
		CategoryID:            a.CategoryID,
		Identification:        a.Identification,
		AcquisitionValue:      a.AcquisitionValue,
		AcquisitionDate:       civildate.OfPtr(a.AcquisitionDate),
		CurrentEstimatedValue: a.CurrentEstimatedValue,
		Status:                a.Status,
		HasDebt:               a.HasDebt,
		DebtAmount:            a.DebtAmount,
		Notes:                 a.Notes,
		Attributes:            a.Attributes,
		CreatedAt:             a.CreatedAt,
	}
}

func toDTOs(as []Asset) []AssetDTO {
	dtos := make([]AssetDTO, len(as))
	for i, a := range as {
		dtos[i] = toDTO(a)
	}
	return dtos
}
