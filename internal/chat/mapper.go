package chat

func toDTO(m Message) MessageDTO {
	return MessageDTO{
		ID:        m.ID,
		Role:      m.Role,
		Content:   m.Content,
		ModelUsed: m.ModelUsed,
		ToolCalls: m.ToolCalls,
		CreatedAt: m.CreatedAt,
	}
}

func toDTOs(ms []Message) []MessageDTO {
	dtos := make([]MessageDTO, len(ms))
	for i, m := range ms {
		dtos[i] = toDTO(m)
	}
	return dtos
}
