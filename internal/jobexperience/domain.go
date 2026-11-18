// Package jobexperience is structured career history (company, title, level, salary, period) for a
// person — the "Carreira" screen's data source. Replaces the old approach of deriving career facts
// from generic person_information key/value rows plus paystub documents: this module gives it a
// real table with typed fields instead of free text.
package jobexperience

import (
	"time"

	"github.com/google/uuid"
)

// ContractType says whether payroll tax withholding (INSS/IRRF) even applies to Salary — only
// meaningful for CLT. PJ salaries are gross of whatever tax regime the professional files under
// (Simples Nacional, MEI, ...), which this module can't know, so ContractPJ instead applies a flat
// estimated rate (see internal/payroll).
type ContractType string

const (
	ContractCLT ContractType = "CLT"
	ContractPJ  ContractType = "PJ"
)

// Experience mirrors the job_experiences table (migration 000020_job_experiences.up.sql, extended
// by 000027_job_experiences_contract_currency.up.sql). EndDate == nil means "current job" —
// IsCurrent is derived at the DTO layer (mapper.go), never persisted. ResumeDocumentID optionally
// links a filed résumé (documents.Origin = RESUME); it is never auto-extracted, purely a reference
// kept alongside the manually-entered structured data.
type Experience struct {
	ID               uuid.UUID
	PersonID         uuid.UUID
	Company          string
	Title            string
	PositionLevel    *string
	Salary           *string // numeric(15,2) as string, same convention as every other money field
	ContractType     ContractType
	StartDate        time.Time
	EndDate          *time.Time
	Notes            *string
	ResumeDocumentID *uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time

	// USDAmount/ExchangeRate are optional and only meaningful together — same convention as
	// internal/income's Entry (migration 000019): Salary (BRL) stays the source of truth for every
	// KPI/report, these two are purely informational for a foreign-currency-paid position.
	USDAmount    *string
	ExchangeRate *string
}
