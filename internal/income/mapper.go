package income

import "github.com/atlas/atlas-go/internal/platform/civildate"

func toDTO(e Entry) EntryDTO {
	return EntryDTO{
		ID:             e.ID,
		DocumentID:     e.DocumentID,
		CategoryID:     e.CategoryID,
		PersonID:       e.PersonID,
		Amount:         e.Amount,
		IncomeDate:     civildate.Of(e.IncomeDate),
		OriginalDate:   civildate.OfPtr(e.OriginalDate),
		Description:    e.Description,
		Reviewed:       e.Reviewed,
		CreatedAt:      e.CreatedAt,
		Source:         e.Source,
		CategorySource: e.CategorySource,
		USDAmount:      e.USDAmount,
		ExchangeRate:   e.ExchangeRate,
	}
}

func toDTOs(es []Entry) []EntryDTO {
	dtos := make([]EntryDTO, len(es))
	for i, e := range es {
		dtos[i] = toDTO(e)
	}
	return dtos
}
