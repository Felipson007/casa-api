package notifications

func toDTO(n Notification) NotificationDTO {
	return NotificationDTO{
		ID:        n.ID,
		Type:      n.Type,
		Title:     n.Title,
		Message:   n.Message,
		Link:      n.Link,
		Read:      n.Read,
		CreatedAt: n.CreatedAt,
	}
}

func toDTOs(ns []Notification) []NotificationDTO {
	dtos := make([]NotificationDTO, len(ns))
	for i, n := range ns {
		dtos[i] = toDTO(n)
	}
	return dtos
}

func toConfigDTO(c Configuration) ConfigurationDTO {
	return ConfigurationDTO{
		ID:            c.ID,
		RetentionDays: c.RetentionDays,
		CreatedAt:     c.CreatedAt,
	}
}
