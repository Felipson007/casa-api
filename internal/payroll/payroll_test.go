package payroll

import "testing"

// TestEstimateNetCLT_5000 mirrors the worked example found in public sources for the 2026 INSS
// table: gross R$ 5,000.00 CLT deducts ~R$ 501,51 of INSS. Also exercises Lei 15.270/2025: this
// income sits below the 5,000 exemption cutoff (measured on the post-INSS taxable base, which is
// below 5,000), so IRRF should be fully zeroed.
func TestEstimateNetCLT_5000(t *testing.T) {
	d, err := EstimateNet("5000.00", "CLT")
	if err != nil {
		t.Fatalf("EstimateNet: %v", err)
	}
	if d.INSS != "501.51" {
		t.Errorf("INSS = %s, want 501.51", d.INSS)
	}
	if d.IRRF != "0.00" {
		t.Errorf("IRRF = %s, want 0.00 (below Lei 15.270/2025 exemption cutoff)", d.IRRF)
	}
}

// TestEstimateNetCLT_AboveReductionCeiling checks a high salary gets the full classic-table IRRF,
// with no Lei 15.270/2025 reduction (taxable base clearly above 7,350).
func TestEstimateNetCLT_AboveReductionCeiling(t *testing.T) {
	d, err := EstimateNet("15000.00", "CLT")
	if err != nil {
		t.Fatalf("EstimateNet: %v", err)
	}
	// INSS caps at the 8,475.55 ceiling regardless of gross, so it must be the same as for any
	// salary at or above the ceiling.
	if d.INSS != "988.09" {
		t.Errorf("INSS = %s, want 988.09 (ceiling reached)", d.INSS)
	}
	if d.IRRF == "0.00" {
		t.Error("IRRF = 0.00, want a real charge — this income is far above the reduction ceiling")
	}
}

// TestEstimateNetPJ confirms PJ never gets INSS/IRRF broken out — only the flat estimated rate.
func TestEstimateNetPJ(t *testing.T) {
	d, err := EstimateNet("10000.00", "PJ")
	if err != nil {
		t.Fatalf("EstimateNet: %v", err)
	}
	if d.INSS != "0.00" || d.IRRF != "0.00" {
		t.Errorf("PJ must not carry payroll withholding: INSS=%s IRRF=%s", d.INSS, d.IRRF)
	}
	if d.Total != "1100.00" {
		t.Errorf("Total = %s, want 1100.00 (11%% flat estimate)", d.Total)
	}
	if d.Net != "8900.00" {
		t.Errorf("Net = %s, want 8900.00", d.Net)
	}
}

// TestEstimateNetNeverExceedsGross guards the invariant that discounts can never make Net negative
// or exceed Gross, across a spread of salaries including edge cases at bracket boundaries.
func TestEstimateNetNeverExceedsGross(t *testing.T) {
	for _, gross := range []string{"0.00", "1621.00", "2902.84", "4354.27", "8475.55", "50000.00"} {
		d, err := EstimateNet(gross, "CLT")
		if err != nil {
			t.Fatalf("EstimateNet(%s): %v", gross, err)
		}
		g := rat(gross)
		net := rat(d.Net)
		if net.Cmp(g) > 0 {
			t.Errorf("gross=%s: Net %s exceeds Gross", gross, d.Net)
		}
		if net.Sign() < 0 {
			t.Errorf("gross=%s: Net %s is negative", gross, d.Net)
		}
	}
}
