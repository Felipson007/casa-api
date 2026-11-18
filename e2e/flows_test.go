//go:build e2e

package e2e

import (
	"net/http"
	"testing"
)

func TestSemSessao_RotasProtegidasDevolvem401(t *testing.T) {
	c := newClient(t)
	for _, path := range []string{"/api/auth/me", "/api/expenses", "/api/goals", "/api/dashboard/resumo"} {
		if got, body := c.do(http.MethodGet, path, nil); got != http.StatusUnauthorized {
			t.Errorf("GET %s sem sessão: status %d, esperado 401; corpo: %s", path, got, body)
		}
	}
}

func TestCadastroELogin(t *testing.T) {
	c := signup(t, "cadastro", "cadastro@example.com")

	var me struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	c.mustStatus(http.StatusOK, http.MethodGet, "/api/auth/me", nil, &me)
	if me.Email != "cadastro@example.com" || me.Role != "OWNER" {
		t.Fatalf("me = %+v, esperado o dono recém-cadastrado", me)
	}

	// Um navegador novo entra com e-mail e senha e cai na mesma conta.
	outro := newClient(t)
	outro.mustStatus(http.StatusOK, http.MethodPost, "/api/auth/login",
		map[string]string{"email": "cadastro@example.com", "senha": "senha-segura-123"}, nil)
	outro.mustStatus(http.StatusOK, http.MethodGet, "/api/auth/me", nil, &me)
	if me.Email != "cadastro@example.com" {
		t.Fatalf("login abriu a conta errada: %+v", me)
	}

	if got, _ := newClient(t).do(http.MethodPost, "/api/auth/login",
		map[string]string{"email": "cadastro@example.com", "senha": "senha-errada-999"}); got == http.StatusOK {
		t.Fatal("login com senha errada foi aceito")
	}
}

func TestGastos_CriarListarApagar(t *testing.T) {
	c := principal(t)

	var criado struct {
		ID     string `json:"id"`
		Amount string `json:"amount"`
	}
	c.mustStatus(http.StatusCreated, http.MethodPost, "/api/expenses", map[string]any{
		"amount":      "189.90",
		"expenseDate": "2026-11-10",
		"description": "Mercado do mês",
	}, &criado)
	if criado.ID == "" {
		t.Fatal("gasto criado sem id")
	}

	var lista []comID
	c.mustStatus(http.StatusOK, http.MethodGet, "/api/expenses", nil, &lista)
	if !contemID(lista, criado.ID) {
		t.Fatalf("o gasto %s não apareceu na lista: %+v", criado.ID, lista)
	}

	c.mustStatus(http.StatusNoContent, http.MethodDelete, "/api/expenses/"+criado.ID, nil, nil)
	c.mustStatus(http.StatusOK, http.MethodGet, "/api/expenses", nil, &lista)
	if contemID(lista, criado.ID) {
		t.Fatalf("o gasto %s continuou na lista depois de apagado", criado.ID)
	}
}

func TestGastos_ValidacaoRejeitaValorInvalido(t *testing.T) {
	c := principal(t)
	got, body := c.do(http.MethodPost, "/api/expenses", map[string]any{
		"amount":      "não é número",
		"expenseDate": "2026-11-10",
	})
	if got < 400 || got >= 500 {
		t.Fatalf("valor inválido: status %d, esperado 4xx; corpo: %s", got, body)
	}
}

func TestMetas_EntradaDaCasa(t *testing.T) {
	c := principal(t)

	var meta struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		TargetAmount string `json:"targetAmount"`
	}
	c.mustStatus(http.StatusCreated, http.MethodPost, "/api/goals", map[string]any{
		"name":                "Entrada da casa",
		"kind":                "PURCHASE",
		"targetAmount":        "145000",
		"targetDate":          "2029-11-01",
		"monthlyContribution": "3500",
	}, &meta)
	if meta.Name != "Entrada da casa" {
		t.Fatalf("meta criada = %+v", meta)
	}

	var metas []comID
	c.mustStatus(http.StatusOK, http.MethodGet, "/api/goals", nil, &metas)
	if !contemID(metas, meta.ID) {
		t.Fatalf("a meta %s não apareceu na lista: %+v", meta.ID, metas)
	}
}

func TestIsolamento_UmEspacoNaoVeOGastoDoOutro(t *testing.T) {
	a := principal(t)
	b := signup(t, "isolado", "isolado@example.com")

	var criado struct {
		ID string `json:"id"`
	}
	a.mustStatus(http.StatusCreated, http.MethodPost, "/api/expenses",
		map[string]any{"amount": "50.00", "expenseDate": "2026-11-10"}, &criado)

	var lista []comID
	b.mustStatus(http.StatusOK, http.MethodGet, "/api/expenses", nil, &lista)
	if len(lista) != 0 {
		t.Fatalf("o espaço B enxergou gastos do espaço A: %+v", lista)
	}
	if got, _ := b.do(http.MethodGet, "/api/expenses/"+criado.ID, nil); got != http.StatusNotFound {
		t.Fatalf("o espaço B abriu o gasto do espaço A pelo id: status %d", got)
	}
}

func TestModulosRemovidos_Devolvem404(t *testing.T) {
	c := principal(t)
	for _, path := range []string{
		"/api/pets",
		"/api/life-map/preferences",
		"/api/dashboard/calendar?from=2026-01-01&to=2026-02-01",
		"/api/calendar/events",
		"/api/integrations/google-calendar/status",
	} {
		if got, _ := c.do(http.MethodGet, path, nil); got != http.StatusNotFound {
			t.Errorf("GET %s: status %d, esperado 404 (módulo removido)", path, got)
		}
	}
}

func TestModulosMantidos_Respondem(t *testing.T) {
	c := principal(t)
	for _, path := range []string{
		"/api/dashboard/resumo", "/api/income", "/api/categories", "/api/loans",
		"/api/people", "/api/health/records", "/api/documents",
	} {
		if got, body := c.do(http.MethodGet, path, nil); got != http.StatusOK {
			t.Errorf("GET %s: status %d, esperado 200; corpo: %s", path, got, body)
		}
	}
}

type comID struct {
	ID string `json:"id"`
}

func contemID(itens []comID, id string) bool {
	for _, item := range itens {
		if item.ID == id {
			return true
		}
	}
	return false
}
