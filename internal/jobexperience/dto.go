package jobexperience

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// PayrollDiscountsDTO breaks down the estimated payroll tax withholding on Salary — see
// internal/payroll's doc comment for what's a real bracket-by-bracket calculation (CLT) versus a
// flat estimate (PJ). Omitted entirely when Salary is nil (nothing to estimate).
type PayrollDiscountsDTO struct {
	INSS      string `json:"inss"`
	IRRF      string `json:"irrf"`
	Total     string `json:"total"`
	NetSalary string `json:"netSalary"`
}

// ExperienceDTO is the JSON shape returned to clients. IsCurrent is derived (EndDate == nil), never
// stored — see Experience's doc comment. PayrollDiscounts is computed on read, never persisted —
// same reasoning (it's a function of Salary/ContractType, recomputing it keeps it from drifting out
// of sync if the tax tables are ever updated).
type ExperienceDTO struct {
	ID               uuid.UUID            `json:"id"`
	PersonID         uuid.UUID            `json:"personId"`
	Company          string               `json:"company"`
	Title            string               `json:"title"`
	PositionLevel    *string              `json:"positionLevel,omitempty"`
	Salary           *string              `json:"salary,omitempty"`
	ContractType     string               `json:"contractType"`
	PayrollDiscounts *PayrollDiscountsDTO `json:"payrollDiscounts,omitempty"`
	USDAmount        *string              `json:"usdAmount,omitempty"`
	ExchangeRate     *string              `json:"exchangeRate,omitempty"`
	StartDate        civildate.Date       `json:"startDate"`
	EndDate          *civildate.Date      `json:"endDate,omitempty"`
	IsCurrent        bool                 `json:"isCurrent"`
	Notes            *string              `json:"notes,omitempty"`
	ResumeDocumentID *uuid.UUID           `json:"resumeDocumentId,omitempty"`
	CreatedAt        time.Time            `json:"createdAt"`
	UpdatedAt        time.Time            `json:"updatedAt"`
}

// CreateExperienceRequest is the JSON body accepted by POST /api/job-experiences.
type CreateExperienceRequest struct {
	PersonID         uuid.UUID       `json:"personId" validate:"required"`
	Company          string          `json:"company" validate:"required,max=200"`
	Title            string          `json:"title" validate:"required,max=200"`
	PositionLevel    *string         `json:"positionLevel" validate:"omitempty,max=200"`
	Salary           *string         `json:"salary" validate:"omitempty,numeric"`
	ContractType     string          `json:"contractType" validate:"omitempty,oneof=CLT PJ"`
	USDAmount        *string         `json:"usdAmount" validate:"omitempty,numeric"`
	ExchangeRate     *string         `json:"exchangeRate" validate:"omitempty,numeric"`
	StartDate        civildate.Date  `json:"startDate" validate:"required"`
	EndDate          *civildate.Date `json:"endDate"`
	Notes            *string         `json:"notes"`
	ResumeDocumentID *uuid.UUID      `json:"resumeDocumentId"`
}

// UpdateExperienceRequest is the JSON body accepted by PUT /api/job-experiences/{id} — full
// replace, same convention as every other module's Update (PersonID is not re-settable, mirrors
// clients.UpdateClientRequest not carrying an owner change either).
type UpdateExperienceRequest struct {
	Company          string          `json:"company" validate:"required,max=200"`
	Title            string          `json:"title" validate:"required,max=200"`
	PositionLevel    *string         `json:"positionLevel" validate:"omitempty,max=200"`
	Salary           *string         `json:"salary" validate:"omitempty,numeric"`
	ContractType     string          `json:"contractType" validate:"omitempty,oneof=CLT PJ"`
	USDAmount        *string         `json:"usdAmount" validate:"omitempty,numeric"`
	ExchangeRate     *string         `json:"exchangeRate" validate:"omitempty,numeric"`
	StartDate        civildate.Date  `json:"startDate" validate:"required"`
	EndDate          *civildate.Date `json:"endDate"`
	Notes            *string         `json:"notes"`
	ResumeDocumentID *uuid.UUID      `json:"resumeDocumentId"`
}
