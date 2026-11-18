package pets

import "github.com/atlas/atlas-go/internal/platform/civildate"

func toDTO(p Pet) PetDTO {
	return PetDTO{
		ID:        p.ID,
		Name:      p.Name,
		Species:   p.Species,
		Breed:     p.Breed,
		BirthDate: civildate.OfPtr(p.BirthDate),
		Notes:     p.Notes,
		Active:    p.Active,
		CreatedAt: p.CreatedAt,
	}
}

func toDTOs(ps []Pet) []PetDTO {
	dtos := make([]PetDTO, len(ps))
	for i, p := range ps {
		dtos[i] = toDTO(p)
	}
	return dtos
}
