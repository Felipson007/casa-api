package personinfo

func toDTO(i Information) InformationDTO {
	return InformationDTO{
		ID: i.ID, PersonID: i.PersonID, Category: i.Category, Key: i.Key, Value: i.Value,
		DocumentID: i.DocumentID, Origin: i.Origin, DocumentType: i.DocumentType, CreatedAt: i.CreatedAt,
	}
}

func toDTOs(is []Information) []InformationDTO {
	dtos := make([]InformationDTO, len(is))
	for i, item := range is {
		dtos[i] = toDTO(item)
	}
	return dtos
}

func toHistoryDTO(h HistoryEntry) HistoryEntryDTO {
	return HistoryEntryDTO{
		ID: h.ID, Category: h.Category, Key: h.Key, Value: h.Value,
		DocumentType: h.DocumentType, ChangedAt: h.ChangedAt,
	}
}

func toHistoryDTOs(hs []HistoryEntry) []HistoryEntryDTO {
	dtos := make([]HistoryEntryDTO, len(hs))
	for i, h := range hs {
		dtos[i] = toHistoryDTO(h)
	}
	return dtos
}
