package integrations

func toExternalDTO(e ExternalIntegration) ExternalIntegrationDTO {
	return ExternalIntegrationDTO{
		ID:               e.ID,
		Type:             e.Type,
		ConnectedAccount: e.ConnectedAccount,
		Models:           e.Models,
		CreatedAt:        e.CreatedAt,
	}
}

func toExternalDTOs(es []ExternalIntegration) []ExternalIntegrationDTO {
	dtos := make([]ExternalIntegrationDTO, len(es))
	for i, e := range es {
		dtos[i] = toExternalDTO(e)
	}
	return dtos
}

func toSystemDTO(s SystemIntegration) SystemIntegrationDTO {
	return SystemIntegrationDTO{
		ID:               s.ID,
		Type:             s.Type,
		ConnectedAccount: s.ConnectedAccount,
		CreatedAt:        s.CreatedAt,
	}
}
