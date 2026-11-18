package loans

import "github.com/atlas/atlas-go/internal/platform/civildate"

func toDTO(l Loan) LoanDTO {
	return LoanDTO{
		ID:                  l.ID,
		ClientID:            l.ClientID,
		PrincipalAmount:     l.PrincipalAmount,
		MonthlyInterestRate: l.MonthlyInterestRate,
		PaymentType:         l.PaymentType,
		InstallmentCount:    l.InstallmentCount,
		ContractDate:        civildate.Of(l.ContractDate),
		Status:              l.Status,
		OutstandingBalance:  l.OutstandingBalance,
		DueDay:              l.DueDay,
		PaidOffDate:         civildate.OfPtr(l.PaidOffDate),
		LegacySource:        l.LegacySource,
		CreatedAt:           l.CreatedAt,
	}
}

func toDTOs(ls []Loan) []LoanDTO {
	dtos := make([]LoanDTO, len(ls))
	for i, l := range ls {
		dtos[i] = toDTO(l)
	}
	return dtos
}

func toInstallmentDTO(i Installment) InstallmentDTO {
	return InstallmentDTO{
		ID:                 i.ID,
		LoanID:             i.LoanID,
		InstallmentNumber:  i.InstallmentNumber,
		DueDate:            civildate.Of(i.DueDate),
		InstallmentAmount:  i.InstallmentAmount,
		OutstandingBalance: i.OutstandingBalance,
		PrincipalAmount:    i.PrincipalAmount,
		InterestAmount:     i.InterestAmount,
		Status:             i.Status,
		CreatedAt:          i.CreatedAt,
	}
}

func toInstallmentDTOs(is []Installment) []InstallmentDTO {
	dtos := make([]InstallmentDTO, len(is))
	for i, inst := range is {
		dtos[i] = toInstallmentDTO(inst)
	}
	return dtos
}

func toTransactionDTO(t Transaction) TransactionDTO {
	return TransactionDTO{
		ID:            t.ID,
		ClientID:      t.ClientID,
		LoanID:        t.LoanID,
		InstallmentID: t.InstallmentID,
		Type:          t.Type,
		Amount:        t.Amount,
		PaymentDate:   civildate.Of(t.PaymentDate),
		Description:   t.Description,
		LegacySource:  t.LegacySource,
		CreatedAt:     t.CreatedAt,
	}
}

func toTransactionDTOs(ts []Transaction) []TransactionDTO {
	dtos := make([]TransactionDTO, len(ts))
	for i, t := range ts {
		dtos[i] = toTransactionDTO(t)
	}
	return dtos
}
