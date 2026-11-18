package categories

func toDTO(c Category) CategoryDTO {
	return CategoryDTO{
		ID:               c.ID,
		Name:             c.Name,
		ParentCategoryID: c.ParentCategoryID,
		Type:             c.Type,
		CreatedAt:        c.CreatedAt,
	}
}

func toDTOs(cs []Category) []CategoryDTO {
	dtos := make([]CategoryDTO, len(cs))
	for i, c := range cs {
		dtos[i] = toDTO(c)
	}
	return dtos
}
