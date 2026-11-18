//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"sync"
	"testing"
)

// client é um usuário navegando no app: guarda o cookie de sessão e manda o Origin do próprio app,
// como o navegador faria (o guard de CSRF compara esse header).
type client struct {
	t    *testing.T
	http *http.Client
}

func newClient(t *testing.T) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, http: &http.Client{Jar: jar}}
}

// do faz a requisição e devolve status e corpo. body nil = sem corpo.
func (c *client) do(method, path string, body any) (int, []byte) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, baseURL+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Origin", baseURL)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp.StatusCode, data
}

// mustStatus faz a requisição, falha o teste se o status não for o esperado e decodifica o corpo em out (se não for nil).
func (c *client) mustStatus(want int, method, path string, body, out any) {
	c.t.Helper()
	got, data := c.do(method, path, body)
	if got != want {
		c.t.Fatalf("%s %s: status %d, esperado %d; corpo: %s", method, path, got, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			c.t.Fatalf("%s %s: decodificando resposta: %v; corpo: %s", method, path, err, data)
		}
	}
}

// signup cria um espaço novo (tenant) com um dono e deixa o cliente logado nele.
func signup(t *testing.T, slug, email string) *client {
	t.Helper()
	c := newClient(t)
	c.mustStatus(http.StatusCreated, http.MethodPost, "/api/auth/signup", map[string]string{
		"slug":  slug,
		"email": email,
		"senha": "senha-segura-123",
		"nome":  "Teste E2E",
	}, nil)
	return c
}

// O cadastro tem limite de 5 por hora por IP (proteção da própria API). Por isso os testes que não
// precisam de um espaço vazio só para eles compartilham uma conta principal, e as asserções olham o
// id do que o teste criou em vez do tamanho total das listas.
var (
	principalOnce sync.Once
	principalJar  http.CookieJar
)

// principal devolve um cliente logado na conta compartilhada dos testes.
func principal(t *testing.T) *client {
	t.Helper()
	principalOnce.Do(func() {
		principalJar = signup(t, "principal", "principal@example.com").http.Jar
	})
	if principalJar == nil {
		t.Fatal("o cadastro da conta principal falhou num teste anterior")
	}
	return &client{t: t, http: &http.Client{Jar: principalJar}}
}
