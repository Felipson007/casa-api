package jobexperience

import (
	"github.com/atlas/atlas-go/internal/payroll"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/atlas/atlas-go/internal/platform/logging"
)

func toDTO(e Experience) ExperienceDTO {
	return ExperienceDTO{
		ID:               e.ID,
		PersonID:         e.PersonID,
		Company:          e.Company,
		Title:            e.Title,
		PositionLevel:    e.PositionLevel,
		Salary:           e.Salary,
		ContractType:     string(e.ContractType),
		PayrollDiscounts: payrollDiscountsDTO(e.Salary, e.ContractType),
		USDAmount:        e.USDAmount,
		ExchangeRate:     e.ExchangeRate,
		StartDate:        civildate.Of(e.StartDate),
		EndDate:          civildate.OfPtr(e.EndDate),
		IsCurrent:        e.EndDate == nil,
		Notes:            e.Notes,
		ResumeDocumentID: e.ResumeDocumentID,
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}
}

// payrollDiscountsDTO computes the INSS/IRRF (or PJ flat-rate) breakdown for display — nil when
// there's no salary to estimate against. A malformed stored salary (shouldn't happen — Salary is
// validated numeric on write) logs and degrades to nil rather than failing the whole response, same
// "never let a derived/best-effort field break the primary read" discipline as the rest of this
// codebase (e.g. dashboard's per-dependency isolation).
func payrollDiscountsDTO(salary *string, contractType ContractType) *PayrollDiscountsDTO {
	if salary == nil {
		return nil
	}
	d, err := payroll.EstimateNet(*salary, string(contractType))
	if err != nil {
		logging.New().Warn("jobexperience: failed to estimate payroll discounts", "error", err)
		return nil
	}
	return &PayrollDiscountsDTO{INSS: d.INSS, IRRF: d.IRRF, Total: d.Total, NetSalary: d.Net}
}

func toDTOs(es []Experience) []ExperienceDTO {
	dtos := make([]ExperienceDTO, len(es))
	for i, e := range es {
		dtos[i] = toDTO(e)
	}
	return dtos
}
