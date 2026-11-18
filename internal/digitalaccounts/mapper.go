package digitalaccounts

func toDTO(a Account) AccountDTO {
	return AccountDTO{
		ID:             a.ID,
		PersonID:       a.PersonID,
		ServiceName:    a.ServiceName,
		Category:       a.Category,
		EmailUsed:      a.EmailUsed,
		MFAMethod:      a.MFAMethod,
		RecoveryNotes:  a.RecoveryNotes,
		Status:         a.Status,
		LastReviewedAt: a.LastReviewedAt,
		CreatedAt:      a.CreatedAt,
	}
}

func toDTOs(as []Account) []AccountDTO {
	dtos := make([]AccountDTO, len(as))
	for i, a := range as {
		dtos[i] = toDTO(a)
	}
	return dtos
}
