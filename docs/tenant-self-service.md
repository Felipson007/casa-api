# Tenant self-service — schema por tenant (revisão do D-001)

> **Status:** proposto (implementação em fases). Substitui parcialmente o D-001
> (`decisions/DECISIONS.md`): o app deixa de ser estritamente single-tenant. O **modelo** escolhido
> NÃO é `tenant_id` + RLS (o que o D-001 removeu), e sim **um schema Postgres por tenant**, o que
> preserva a maior parte das simplificações do D-001 (nenhuma coluna `tenant_id`, nenhuma policy).

## Contexto

O Atlas nasceu single-tenant por decisão de produto (uso pessoal/household). Agora há um segundo
usuário real que precisa dos **próprios dados isolados**, e existe a possibilidade de virar SaaS no
futuro. Para SaaS, **banco por tenant** escala mal (um pool por banco → `max_connections`,
migrations N vezes, provisionamento/backup N vezes). **Schema por tenant** mantém 1 banco/1 pool,
isola os dados, e permite FK cross-schema — inclusive o `chat_messages.app_user_id → app_users`.

O usuário também pediu que **o dado existente dele seja migrado para um schema próprio** (sair do
`public`), com **backup obrigatório antes**.

## Decisão

1. Tabelas de **controle** ficam no schema `public`:
   `app_users`, `member_permissions`, `password_reset_tokens`, `sessions` e a nova `tenants`.
2. Cada **tenant** tem um schema Postgres `tenant_<slug>` com **todas as tabelas de domínio**.
3. A resolução tenant→schema vive na borda: a sessão carrega `app_users.tenant_id`, que aponta para
   `tenants.schema_name`; um middleware injeta esse schema no `context`.
4. **Toda** query de domínio já passa por `db.WithTx`; ele passa a aplicar
   `set_config('search_path', <schema>, true)` (transaction-scoped) quando há schema no contexto.
   Queries de controle usam o novo `db.WithControlTx`, que ignora o schema (sempre `public`).
5. **Self-service:** `POST /api/auth/signup` cria o schema, roda as migrations de tenant e cria o
   usuário OWNER daquele tenant. Gated por `SIGNUP_ENABLED` (+ `SIGNUP_INVITE_TOKEN` opcional).
6. **Sem RLS e sem `tenant_id`.** Isolamento = namespace do schema + `search_path` centralizado.
   Uma query que não passe pelo `WithTx` é o risco residual; o gate é de review/lint.
   (Achado no cutover: `settings`, `jobs`, `backup` e `curator` acessavam o `pool` direto e
   furavam o `search_path`; foram convertidos para `db.WithTx`.)
7. **Envelope encryption por tenant** (`internal/platform/keyring`): uma KEK mestra
   (`TENANT_MASTER_KEY`, fora do banco) embrulha uma DEK por tenant, guardada em
   `tenants.dek_wrapped`. Um dump/backup sozinho não descriptografa nada; apagar a DEK faz
   crypto-shredding. Modelo "trusted operator": o operador consegue descriptografar durante o
   processamento, mas migrar para KMS/BYOK depois é mudança de gestão de chave, não de schema.
   A adoção da cripto por módulo (uploads/documentos, colunas sensíveis, credenciais de integração)
   é incremental e não altera o modelo de dados.

## Estado incremental (baixo risco)

Para não quebrar a produção, a implementação é incremental:

- **Agora:** `public` continua com controle + domínio do dono (intocado). Novos tenants ganham
  schema próprio via `internal/tenantmigrations`. O dono resolve para `schema_name = 'public'`.
- **Depois (cutover agendado, com backup):** mover as tabelas de domínio do dono de `public` para
  `tenant_<slug-do-dono>` e trocar `tenants.schema_name` de `public` para esse schema. A partir daí
  `public` só tem controle.

Enquanto os dois estados coexistirem, migrations de domínio novas precisam entrar **nos dois
lugares** (`internal/migrations` para o `public` do dono; `internal/tenantmigrations` para os novos
tenants). Após o cutover, `internal/migrations` fica só com controle e
`internal/controlmigrations` assume. Isso está documentado como dívida temporária.

## Estrutura de migrations

| Dir | Aplica em | Conteúdo |
|---|---|---|
| `internal/migrations` | `public` (dono, estado atual) | tudo (controle + domínio) — mantido até o cutover |
| `internal/tenantmigrations` | cada `tenant_<slug>` | domínio puro (baseline esmagado dos 28 migrations) |
| `internal/controlmigrations` | `public` (após cutover) | controle puro + `tenants` |

O `search_path` para rodar as migrations de um schema é passado no DSN do golang-migrate
(`?search_path=tenant_x&x-migrations-table=schema_migrations`), então cada schema tem sua própria
tabela de versão.

## Cutover do dono (runbook, executado no ambiente dele)

> Nada disso roda automaticamente. O dono executa com backup e dry-run.

1. **Backup obrigatório:** `pg_dump -F c` do banco atual (e restaurar num banco de verificação).
2. Rodar o ETL `cmd/tenant-migrate --dry-run` (lista o que moveria; não altera nada).
3. Rodar sem `--dry-run`: cria `tenant_<slug>`, move as tabelas de domínio
   (`ALTER TABLE ... SET SCHEMA`), move a view `v_person_relationships`, ajusta a versão das
   migrations do schema novo (baseline) e atualiza `tenants.schema_name`.
4. Trocar a config (`TENANT_DEFAULT_SCHEMA=tenant_<slug>`) e reiniciar.
5. Validar contagens por tabela e uma amostra de lançamentos antes de descartar o backup.

Os triggers `trg_expenses_bank_projection_deleted` / `trg_income_bank_projection_deleted` movem
junto com as tabelas; a função `bank_projection_deleted()` fica no schema do tenant e resolve as
tabelas via `search_path` em runtime.

## Fases

- **Fase 1 (este trabalho):** `db` schema-aware, pacote `tenancy`, migrations de tenant, signup.
- **Fase 2:** cutover do dono (backup + ETL) e cutover das migrations de controle.
- **Fase 3:** `cmd/worker` por tenant (jobs/backup/rollover) — hoje roda só contra `DATABASE_URL`.
- **Fase 4:** frontend (tela de signup) no repo `app-frontend-atlas`.
