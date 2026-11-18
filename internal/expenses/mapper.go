package expenses

import "github.com/atlas/atlas-go/internal/platform/civildate"

func toDTO(e Expense) ExpenseDTO {
	return ExpenseDTO{
		ID:             e.ID,
		DocumentID:     e.DocumentID,
		CategoryID:     e.CategoryID,
		PersonID:       e.PersonID,
		Amount:         e.Amount,
		ExpenseDate:    civildate.Of(e.ExpenseDate),
		Description:    e.Description,
		Reviewed:       e.Reviewed,
		OriginalDate:   civildate.OfPtr(e.OriginalDate),
		Recurring:      e.Recurring,
		CreatedAt:      e.CreatedAt,
		Source:         e.Source,
		CategorySource: e.CategorySource,
	}
}

func toDTOs(es []Expense) []ExpenseDTO {
	dtos := make([]ExpenseDTO, len(es))
	for i, e := range es {
		dtos[i] = toDTO(e)
	}
	return dtos
}
