# Schema migration map — Java/homelab (postgres-db) → atlas-go (new DB)

This is the single source of truth for converting the **current production database**
(`pandora-homelab-postgres-db-1`, multi-tenant, Portuguese names) into the **new single-tenant
schema** that atlas-go targets. Generated once; every future AI/dev reuses it instead of
re-deriving the mapping. The converted data lives as a fixed fixture in `testdata/seed.sql`
(see `docs/test-seed.md`).

## Global rules applied everywhere (architecture-plan.md section 3 + 10)

- **Single-tenant:** NO `tenant_id` column anywhere. The old DB has exactly one tenant
  (`91fa47fb-1ad7-4c7f-ab1b-315ec44002da`); its rows are carried over with the column dropped.
- **Every timestamp is `timestamptz`** (never bare `timestamp`). Old `created_at`/`data_*` are
  assumed `America/Sao_Paulo` and converted via `AT TIME ZONE 'America/Sao_Paulo'`.
- **Enum-like varchar columns get a `CHECK`** with English `UPPER_SNAKE_CASE` values.
- **Every FK child column gets an index.**
- **`parentesco` (pessoas) is dropped** — replaced by `people.gender` (explicit) + server-side
  derivation in `internal/personrelations`.
- **`saldos_financeiros_historico` stays a typed table** (`financial_balance_history`), NOT
  folded into `audit_logs`. Only `pessoa_informacoes_historico` → `audit_logs`.
- **`tenants` → `app_settings`** (singleton row, boolean PK `true`).
- **`flyway_schema_history` is NOT ported** — golang-migrate uses its own table.

## Table mapping (old → new)

| old (postgres-db) | new (atlas-go) | notes |
| --- | --- | --- |
| `tenants` | `app_settings` | singleton; company_name/pix_key/logo_path carried |
| `app_users` | `app_users` | auth users kept as-is (single-tenant, no tenant_id) |
| `tenant_members` | `member_permissions` | `(user_id, module, can_read, can_write)` — see auth note |
| `password_reset_tokens` | `password_reset_tokens` | unchanged shape |
| `categorias` | `categories` | `categoria_pai_id` → `parent_category_id` (indexed self-FK) |
| `pessoas` | `people` | `parentesco` dropped → `gender`; `immich_person_id` dropped (migration 000031/000030 — Immich proxy removed) |
| `pessoa_relacoes` | `person_relationships` | `de_id/para_id` → `from_person_id/to_person_id`; `MAE_DE/PAI_DE/CONJUGE_DE` → `MOTHER_OF/FATHER_OF/SPOUSE_OF` |
| `pessoa_informacoes` | `person_information` | |
| `pessoa_informacoes_historico` | `audit_logs` | generic edit-trail (table_name, row_id, old_data jsonb) |
| `clientes` | `clients` | |
| `emprestimos` | `loans` | `taxa_juros_mensal numeric(5,2)` → `monthly_interest_rate numeric(12,6)`; `saldo_devedor` → `outstanding_balance` |
| `parcelas` | `installments` | `status` CHECK; `valor_principal/valor_juros` kept |
| `movimentacoes` | `loan_transactions` | `tipo` translated (DISBURSEMENT/INSTALLMENT_PAYMENT/ONE_OFF_PAYMENT/FORGIVENESS/AMORTIZATION) |
| `bens` | `assets` | `atributos jsonb` kept |
| `documentos` | `documents` | |
| `gastos` | `expenses` | |
| `receitas` | `income_entries` | |
| `pets` | `pets` | `especie` → `species` CHECK |
| `registros_pet` | `pet_records` | |
| `registros_saude` | `health_records` | |
| `marcadores_exame_catalogo` | `health_marker_catalog` | |
| `saldos_financeiros` | `financial_balances` | |
| `saldos_financeiros_historico` | `financial_balance_history` | TYPED (client_id, balance, recorded_at) — not audit_logs |
| `notificacoes` | `notifications` | |
| `notificacao_configuracao` | `notification_configuration` | |
| `chat_mensagens` | `chat_messages` | `papel` → `role` CHECK; `ferramentas_chamadas` → `tool_calls jsonb` |
| `ia_configuracoes` | `ai_configurations` | `ordem_provedores` (CSV) → `provider_order jsonb` |
| `integracoes_externas` | `external_integrations` | `modelos` (CSV) → `models jsonb`; `immich_base_url` dropped (migration 000031/000030 — Immich proxy removed) |
| `integracoes_sistema` | `system_integrations` | |
| `backup_configuracao` | `backup_configuration` | |
| `jobs` | `jobs` | already in 000001 (status/lease) |

## Enum value translations (old pt → new en)

- `pessoa_relacoes.tipo`: `MAE_DE`→`MOTHER_OF`, `PAI_DE`→`FATHER_OF`, `CONJUGE_DE`→`SPOUSE_OF`
- `emprestimos.status`: `ATIVO`→`ACTIVE`, `INADIMPLENTE`→`OVERDUE`, `QUITADO`→`PAID_OFF`, `CANCELADO`→`CANCELLED`
- `parcelas.status`: `PENDENTE`→`PENDING`, `PAGO`→`PAID`, `ATRASADO`→`OVERDUE`, `CANCELADO`→`CANCELLED`
- `categorias.tipo`: `GASTO`→`EXPENSE`, `RECEITA`→`INCOME`, `BEM`→`ASSET`
- `movimentacoes.tipo`: `DESEMBOLSO`→`DISBURSEMENT`, `RECEBIMENTO_PARCELA`→`INSTALLMENT_PAYMENT`, `RECEBIMENTO_AVULSO`→`ONE_OFF_PAYMENT`, `PERDAO`→`FORGIVENESS`, `AMORTIZACAO`→`AMORTIZATION`
- `pets.especie`: `CACHORRO`→`DOG`, `GATO`→`CAT`, `PASSARO`→`BIRD`, `PEIXE`→`FISH`, `OUTRO`→`OTHER` (verify against `SELECT DISTINCT` before applying)
- `chat_mensagens.papel`: `USUARIO`→`USER`, `ASSISTENTE`→`ASSISTANT`, `SISTEMA`→`SYSTEM`
- `jobs.status`: `PENDENTE`→`PENDING`, `EM_PROCESSAMENTO`→`PROCESSING`, `CONCLUIDO`→`COMPLETED`, `FALHOU`→`FAILED`

## gender derivation (pessoas.parentesco → people.gender)

Applied ONCE at ETL time as initial data (not a permanent mechanism):
- `MAE`, `FILHA`, `IRMA`, `SOBRINHA`, `SOGRA`, `NETA`, `AVO` (feminine) → `F`
- `PAI`, `FILHO`, `IRMAO`, `SOBRINHO`, `SOGRO`, `NETO`, `AVO` (masculine) → `M`
- `EU_MESMO` → `M` (the user, Gustavo, is male — known fact; do NOT infer for others)
- `OUTRO` → `NULL` (leave nullable, reviewable manually; never invent)
- any person appearing as `MAE_DE`/`FATHER_OF` target's parent edge → `F`/`M` accordingly

## Auth note (section 10.5 of plan)

`member_permissions` is created now (schema ready) but NO rows are seeded unless/until the user
actually grants access to another person. Single-tenant app, no auto-provisioning of members.
