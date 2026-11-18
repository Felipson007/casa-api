package clients

func toDTO(c Client) ClientDTO {
	return ClientDTO{
		ID:        c.ID,
		Name:      c.Name,
		Phone:     c.Phone,
		Email:     c.Email,
		CPFCNPJ:   c.CPFCNPJ,
		Notes:     c.Notes,
		Active:    c.Active,
		PixKey:    c.PixKey,
		CreatedAt: c.CreatedAt,
	}
}

func toDTOs(cs []Client) []ClientDTO {
	dtos := make([]ClientDTO, len(cs))
	for i, c := range cs {
		dtos[i] = toDTO(c)
	}
	return dtos
}
