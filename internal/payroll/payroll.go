// Package payroll estimates Brazilian CLT payroll tax withholding (INSS + IRRF) on a monthly gross
// salary, to answer "what's my real take-home pay" for the Carreira module's salary KPIs. PJ
// contracts don't have payroll withholding the same way (the professional self-assesses under
// whatever tax regime they filed under — Simples Nacional, MEI, ...), so this package only computes
// a real bracket-by-bracket estimate for CLT; PJ gets a single flat estimated rate instead (see
// EstimateNet's doc comment).
//
// Tables current as of the 2026 fiscal year (Portaria 13/2026 for INSS; Lei 15.270/2025's monthly
// reduction on top of the classic IRRF brackets). Both tables are reissued every year — when they
// change, update inssBrackets/irrfBrackets here, nowhere else in the codebase computes this.
package payroll

import (
	"fmt"
	"math/big"

	"github.com/atlas/atlas-go/internal/platform/money"
)

// bracket is one slice of a progressive table: income up to (and including) Upper is taxed at Rate
// for the portion of income that falls in this bracket (Upper == nil means "no ceiling", the top
// bracket). Deduction is the fixed amount subtracted when using the shortcut single-multiplication
// formula (base*Rate - Deduction) instead of summing every lower bracket — the two are
// mathematically equivalent for a correctly-built table.
type bracket struct {
	Upper     *big.Rat // nil = no ceiling (top bracket)
	Rate      *big.Rat
	Deduction *big.Rat
}

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic(fmt.Sprintf("payroll: invalid literal %q", s))
	}
	return r
}

func ratPtr(s string) *big.Rat { return rat(s) }

// inssCeiling is the 2026 contribution ceiling — salary above this point stops accruing INSS
// (the fourth bracket's rate never applies beyond it).
var inssCeiling = rat("8475.55")

// inssBrackets: 2026 progressive INSS table (Portaria 13/2026). Each bracket's rate applies only to
// the slice of salary within it — this is NOT "salary * rate", see calculateProgressive.
var inssBrackets = []bracket{
	{Upper: ratPtr("1621.00"), Rate: rat("0.075")},
	{Upper: ratPtr("2902.84"), Rate: rat("0.09")},
	{Upper: ratPtr("4354.27"), Rate: rat("0.12")},
	{Upper: ratPtr("8475.55"), Rate: rat("0.14")},
}

// irrfBrackets: the classic 2026 monthly IRRF table (unchanged in structure since 2023 — only the
// 2026 reduction below is new). Deduction lets base*Rate-Deduction reproduce the bracket-by-bracket
// result in one step (see this package's doc comment).
var irrfBrackets = []bracket{
	{Upper: ratPtr("2428.80"), Rate: rat("0"), Deduction: rat("0")},
	{Upper: ratPtr("2826.65"), Rate: rat("0.075"), Deduction: rat("182.16")},
	{Upper: ratPtr("3751.05"), Rate: rat("0.15"), Deduction: rat("394.16")},
	{Upper: ratPtr("4664.68"), Rate: rat("0.225"), Deduction: rat("675.49")},
	{Upper: nil, Rate: rat("0.275"), Deduction: rat("908.73")},
}

// irrfReductionCutoff/irrfReductionCeiling bound the Lei 15.270/2025 monthly reduction: full
// exemption up to irrfReductionCutoff, a linearly-shrinking reduction up to irrfReductionCeiling,
// none above it.
var (
	irrfReductionCutoff  = rat("5000.00")
	irrfReductionCeiling = rat("7350.00")
	// reductionBase/reductionSlope implement reduction = reductionBase - reductionSlope*income, the
	// published Lei 15.270/2025 formula for the 5,000.01-7,350.00 band.
	reductionBase  = rat("978.62")
	reductionSlope = rat("0.133145")
)

// pjEstimatedRate is a flat, deliberately approximate stand-in for whatever tax regime a PJ
// professional actually files under (Simples Nacional Anexo III/V, MEI, lucro presumido, ...) —
// real PJ taxation varies enormously by revenue, activity, and elections this module has no way to
// know. It exists so "net salary" isn't simply "same as gross" for PJ, at the cost of precision a
// real accountant's figure would have.
var pjEstimatedRate = rat("0.11")

// Discounts is the payroll-tax breakdown for one gross monthly salary.
type Discounts struct {
	INSS  string // "0.00" for PJ (payroll withholding doesn't apply)
	IRRF  string // "0.00" for PJ
	Total string // INSS + IRRF for CLT; the flat pjEstimatedRate estimate for PJ
	Net   string // Gross - Total
}

// EstimateNet computes the estimated net (take-home) monthly salary for grossBRL (a numeric(15,2)
// string) under contractType:
//   - CLT: real progressive INSS + IRRF (with the 2026 Lei 15.270/2025 reduction), bracket by
//     bracket — as precise as the published 2026 tables allow.
//   - PJ: a single flat pjEstimatedRate, since real PJ tax burden depends on a tax regime this
//     module can't observe. Callers should present PJ figures as a rough estimate, not a real
//     withholding calculation, same as CLT's own doc comment says.
func EstimateNet(grossBRL string, contractType string) (Discounts, error) {
	gross, err := money.Parse(grossBRL)
	if err != nil {
		return Discounts{}, fmt.Errorf("payroll: parsing gross salary: %w", err)
	}
	if gross.Sign() <= 0 {
		return Discounts{INSS: money.Zero, IRRF: money.Zero, Total: money.Zero, Net: money.Zero}, nil
	}

	if contractType == "PJ" {
		total := money.Round2(new(big.Rat).Mul(gross, pjEstimatedRate))
		net := new(big.Rat).Sub(gross, total)
		return Discounts{
			INSS:  money.Zero,
			IRRF:  money.Zero,
			Total: money.Format(total),
			Net:   money.Format(net),
		}, nil
	}

	inss := calculateINSS(gross)
	irrf := calculateIRRF(new(big.Rat).Sub(gross, inss))
	total := money.Round2(new(big.Rat).Add(inss, irrf))
	net := new(big.Rat).Sub(gross, total)
	return Discounts{
		INSS:  money.Format(money.Round2(inss)),
		IRRF:  money.Format(money.Round2(irrf)),
		Total: money.Format(total),
		Net:   money.Format(net),
	}, nil
}

// calculateINSS applies the progressive table bracket by bracket (not the deduction shortcut,
// since inssBrackets carries no Deduction field — INSS traditionally isn't published that way).
// Salary above inssCeiling contributes nothing extra past the ceiling.
func calculateINSS(gross *big.Rat) *big.Rat {
	base := gross
	if base.Cmp(inssCeiling) > 0 {
		base = inssCeiling
	}
	total := new(big.Rat)
	lower := new(big.Rat)
	for _, b := range inssBrackets {
		upper := b.Upper
		if base.Cmp(lower) <= 0 {
			break
		}
		sliceTop := upper
		if base.Cmp(sliceTop) < 0 {
			sliceTop = base
		}
		slice := new(big.Rat).Sub(sliceTop, lower)
		if slice.Sign() > 0 {
			total.Add(total, new(big.Rat).Mul(slice, b.Rate))
		}
		lower = upper
	}
	return total
}

// calculateIRRF applies the classic progressive table via the deduction shortcut
// (base*Rate-Deduction), then subtracts the Lei 15.270/2025 monthly reduction on top — the
// reduction can only shrink the tax due, never push it negative (irrfReductionCeiling is where it
// naturally reaches zero, and the final Max(0, ...) is belt-and-suspenders for that boundary).
func calculateIRRF(taxableBase *big.Rat) *big.Rat {
	var due *big.Rat
	for i := len(irrfBrackets) - 1; i >= 0; i-- {
		b := irrfBrackets[i]
		if b.Upper == nil || taxableBase.Cmp(b.Upper) > 0 || i == 0 {
			due = new(big.Rat).Sub(new(big.Rat).Mul(taxableBase, b.Rate), b.Deduction)
			break
		}
	}
	if due == nil || due.Sign() <= 0 {
		return new(big.Rat)
	}

	reduction := irrfReduction(taxableBase)
	due = new(big.Rat).Sub(due, reduction)
	if due.Sign() < 0 {
		return new(big.Rat)
	}
	return due
}

// irrfReduction implements Lei 15.270/2025: full reduction (i.e. the tax due itself, handled by the
// caller reaching zero) up to irrfReductionCutoff, a linearly-shrinking reduction up to
// irrfReductionCeiling, and none above it.
func irrfReduction(taxableBase *big.Rat) *big.Rat {
	if taxableBase.Cmp(irrfReductionCutoff) <= 0 {
		// Below the cutoff the law zeroes IRRF outright — returning a very large reduction here
		// relies on calculateIRRF's caller clamping at zero, keeping this function's contract simple
		// ("how much this reduces the bracket-computed due amount").
		return rat("999999999")
	}
	if taxableBase.Cmp(irrfReductionCeiling) > 0 {
		return new(big.Rat)
	}
	reduction := new(big.Rat).Sub(reductionBase, new(big.Rat).Mul(reductionSlope, taxableBase))
	if reduction.Sign() < 0 {
		return new(big.Rat)
	}
	return reduction
}
