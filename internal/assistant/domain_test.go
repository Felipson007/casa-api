package assistant

import "testing"

// buildTools only constructs toolDef values — the run closures capture the services but are not
// invoked here — so passing nil services is safe and lets this test assert the registry's shape
// without standing up every module. Guards against a finance tool silently dropping out of the
// "IA Atlas" tool-set again (the reason expenses/income were added in the first place).
func TestBuildTools_RegistersFinanceReadTools(t *testing.T) {
	tools := buildTools(nil, nil, nil, nil, nil, nil, nil, nil)

	want := []string{
		"gastos_listar", "gastos_resumo_mensal", "gastos_resumo_anual", "gastos_assinaturas",
		"receitas_listar", "saldos_listar", "metas_listar", "categorias_listar",
		// still there from before:
		"emprestimos_listar", "pessoas_listar", "saude_registros",
	}
	for _, name := range want {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("tool %q not registered", name)
			continue
		}
		if tool.write {
			t.Errorf("tool %q must be read-only, got write=true", name)
		}
	}
}
