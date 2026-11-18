package finance

import "github.com/atlas/atlas-go/internal/platform/civildate"

func toDTO(b Balance) BalanceDTO {
	return BalanceDTO{
		ID:           b.ID,
		PersonID:     b.PersonID,
		Name:         b.Name,
		Type:         b.Type,
		CurrentValue: b.CurrentValue,
		UpdatedOn:    civildate.Of(b.UpdatedOn),
		Notes:        b.Notes,
		CreatedAt:    b.CreatedAt,
		Automatic:    b.BankAccountID != nil,
	}
}

func toDTOs(bs []Balance) []BalanceDTO {
	dtos := make([]BalanceDTO, len(bs))
	for i, b := range bs {
		dtos[i] = toDTO(b)
	}
	return dtos
}

func toHistoryDTO(h BalanceHistoryEntry) BalanceHistoryEntryDTO {
	return BalanceHistoryEntryDTO{
		ID:         h.ID,
		Balance:    h.Balance,
		RecordedOn: civildate.Of(h.RecordedOn),
		CreatedAt:  h.CreatedAt,
	}
}

func toHistoryDTOs(hs []BalanceHistoryEntry) []BalanceHistoryEntryDTO {
	dtos := make([]BalanceHistoryEntryDTO, len(hs))
	for i, h := range hs {
		dtos[i] = toHistoryDTO(h)
	}
	return dtos
}
