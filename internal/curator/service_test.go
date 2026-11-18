package curator

import "testing"

func TestSanitiseInsightsEnforcesClosedSafeShape(t *testing.T) {
	items := sanitiseInsights([]Insight{
		{Type: " health ", Severity: " warning ", Title: " Atenção ", Message: " Confira a referência. "},
		{Type: "loans", Severity: "info", Title: "Carteira", Message: "Concentração saudável."},
		{Type: "UNKNOWN", Severity: "INFO", Title: "invalid", Message: "invalid"},
		{Type: "FINANCIAL", Severity: "DANGER", Title: "invalid", Message: "invalid"},
		{Type: "LOANS", Severity: "WARNING", Title: "Concentração", Message: "Cliente f18dea4e concentra a carteira."},
	})
	if len(items) != 2 {
		t.Fatalf("got %d valid insights, want 2", len(items))
	}
	if items[0].Type != "HEALTH" || items[0].Severity != "WARNING" || items[0].Title != "Atenção" {
		t.Fatalf("unexpected sanitised insight: %+v", items[0])
	}
}

func TestParseModelJSONAcceptsFencedResponse(t *testing.T) {
	var result modelResult
	err := parseModelJSON("```json\n{\"reclassifications\":[],\"insights\":[]}\n```", &result)
	if err != nil {
		t.Fatalf("parseModelJSON returned error: %v", err)
	}
}
