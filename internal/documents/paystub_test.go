package documents

import "testing"

// TestEarningsOvershoot covers the specific paystub failure this check exists for: a base de
// cálculo (Base INSS / Base FGTS / Base IRRF) read as if it were a rubrica. Those sit in the
// holerite's footer, are large, and are inputs to a tax calculation — not money paid. Checking
// only the net pay would miss it whenever a real deduction was also missed, since the two errors
// cancel; checking the earnings side against its own printed subtotal names the failure.
func TestEarningsOvershoot(t *testing.T) {
	// Real numbers from the August 2026 holerite: proventos 12.493,98, descontos 3.840,87,
	// líquido 8.653,11, and a Base INSS of 11.996,34 sitting in the footer waiting to be
	// mistaken for a rubrica.
	const printedProventos = "12493.98"

	if got := earningsOvershoot("12493.98", printedProventos); got != "" {
		t.Errorf("earnings matching the printed total must not be flagged, got %q", got)
	}
	if got := earningsOvershoot("12493.99", printedProventos); got != "" {
		t.Errorf("a one-cent difference is within tolerance, got %q", got)
	}
	// Base INSS swept in as an earning.
	if got := earningsOvershoot("24490.32", printedProventos); got == "" {
		t.Error("earnings far above the printed total must be flagged as a likely base de cálculo")
	}
	// A shortfall is the ordinary missing-line case — reconcileVerdict reports that one.
	if got := earningsOvershoot("11000.00", printedProventos); got != "" {
		t.Errorf("a shortfall is not an overshoot, got %q", got)
	}
	// Nothing to compare against.
	if got := earningsOvershoot("24490.32", ""); got != "" {
		t.Errorf("no printed subtotal means nothing to flag, got %q", got)
	}
	if got := earningsOvershoot("24490.32", "não informado"); got != "" {
		t.Errorf("an unparseable subtotal means nothing to flag, got %q", got)
	}
}
