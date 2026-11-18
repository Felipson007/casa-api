# casa-api

Backend do app de finanças do casal (Go + Postgres), derivado do atlas-go.

## Regras de testes (obrigatórias em toda mudança)

1. **Toda funcionalidade nova ou alterada vem com teste E2E** em `e2e/`, cobrindo o fluxo pela API
   real: cadastro/login, a requisição HTTP e o efeito (o que aparece numa listagem, o que muda no
   banco, o erro esperado). Inclua também o caminho de erro principal (validação, permissão,
   isolamento entre espaços).
2. **Cobertura mínima de 80%**:
   - todo pacote novo entra em `.coverage-strict` e precisa ter pelo menos 80%;
   - pacote antigo que você alterar não pode perder cobertura; quando passar de 80%, inclua-o em
     `.coverage-strict`;
   - a cobertura total nunca cai abaixo do piso em `.coverage-baseline`. Quando ela subir, atualize
     o piso com `scripts/coverage.sh --update-baseline` no mesmo commit.
3. Testes de unidade e de integração continuam ao lado do código (`*_test.go`), como no resto do
   projeto.
4. Antes de dar uma tarefa por terminada, rode a suíte completa (abaixo) e confira que a trava
   passou. Não desligue nem afrouxe a trava para fazer o CI passar.

## Como rodar os testes

Precisa de um Postgres (16) em que o usuário possa criar bancos.

```bash
ADMIN="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
export TEST_DATABASE_URL="$(scripts/prepare-test-db.sh "$ADMIN")"
E2E_POSTGRES_URL="$ADMIN" scripts/coverage.sh
```

- `scripts/prepare-test-db.sh` sobe a API num banco novo e faz um cadastro, para que os testes de
  integração encontrem o schema do tenant já migrado.
- `scripts/coverage.sh` roda unidade + integração + E2E, junta a cobertura das três fontes (os E2E
  sobem a API compilada com `-cover`) e aplica a trava. O relatório fica em `coverage/`.
- Só os E2E: `E2E_POSTGRES_URL="$ADMIN" go test -tags e2e -count=1 ./e2e/...`

O CI (`.github/workflows/ci.yml`) roda exatamente isso em todo push, em qualquer branch.

## Observações

- O cadastro tem limite de 5 por hora por IP. Nos E2E, use `principal(t)` (conta compartilhada) e
  só crie contas novas com `signup` quando o teste precisar de um espaço vazio.
- Módulos removidos do Atlas original: pets, calendário (e as sincronizações com iCloud/Google) e
  mapa da vida. As migrations antigas ficam, porque o golang-migrate depende do histórico.
