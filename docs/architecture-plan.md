# Plano: esqueleto de arquitetura Go (Clean Architecture) para o pandora-loan-helper

> Cópia versionada do plano aprovado (originalmente em
> `C:\Users\Usuario\.claude\plans\atue-como-um-engenheiro-cheeky-donut.md`, fora deste repo). Esta
> cópia é a fonte de verdade a partir de agora — qualquer atualização ao plano deve ser feita aqui.
>
> **Rebrand:** o repo/módulo Go se chama `atlas-go` (módulo `github.com/atlas/atlas-go`), não
> `pandora-go` — o usuário decidiu que a reescrita não se chama mais "Pandora" e sim "Atlas" (o
> pacote Java já usa `br.com.atlas` como raiz, então o nome já existia internamente). Isso já foi
> aplicado em todo o código Go e neste documento; o `pandora-loan-helper` (repo Java atual sendo
> substituído) mantém seu nome — só a reescrita Go foi renomeada.
>
> **Pivô: single-tenant, não SaaS.** Depois de boa parte do isolamento de tenant já estar
> implementado (seção 3 original), o usuário decidiu que esta reescrita **não** vai ser oferecida
> como produto multi-organização — é de uso próprio, com a possibilidade de dar acesso a outras
> pessoas (usuários) no futuro, mas **sem separação de dados por organização/tenant**. Isso
> substitui inteiramente a antiga seção 3 (isolamento de tenant, RLS, `tenant.Scope`) — ver a nova
> seção 3 abaixo.

## Contexto

`pandora-loan-helper` é hoje um backend Java 21 / Spring Boot 3 multi-tenant com 25 módulos de
domínio (`categorias`, `emprestimos`, `gastos`, `receitas`, `saude`, `pets`, `patrimonio`, `bens`,
`documentos`, `ia`, `integracoes`, `notificacoes`, `pessoas`, `pessoainformacoes`,
`pessoarelacoes`, `tenants`, `financeiro`, `relatorios`, `backup`, `extracao`, `eventos` (em
andamento), etc.), seguindo layering estrito Controller → Service → Repository, isolamento de
tenant via `@TenantId` do Hibernate, erros padronizados em RFC 7807, e um pipeline de jobs
assíncronos para relatórios (PDF/xlsx). **A reescrita Go deliberadamente não replica o
multi-tenancy** — ver o pivô acima — mesmo que o Java atual dependa dele; isso é uma diferença de
propósito de produto entre os dois (o Java atendia a ideia original de "ajudante de empréstimo
como serviço", o Go é uso pessoal/doméstico daqui pra frente).

O usuário (engenheiro sênior) quer planejar — ainda sem código — uma reescrita em Go seguindo
Clean Architecture, SOLID e convenções idiomáticas de Go, mantendo paridade com os mecanismos que
já se provaram corretos em produção (tratamento de erro centralizado, padrão de jobs assíncronos),
mas resolvidos de forma idiomática em Go em vez de traduzidos literalmente do padrão Spring. Esta
etapa é só o **esqueleto/arquitetura de referência** (estrutura de pastas, interfaces centrais, um
módulo de exemplo totalmente detalhado — ainda sem portar nenhum módulo real de fato).

O usuário revisou o schema atual (32 tabelas, 48 índices, confirmado batendo com os arquivos reais
em `src/backend/src/main/resources/db/migration/`, hoje até `V56__pessoa_immich.sql`) e trouxe uma
lista concreta de pontos de melhoria — o plano passa a cobrir também **um banco novo, redesenhado**
para a reescrita Go, e a estratégia de migração de dados do banco antigo pro novo (seções 10 e 11).
O usuário pediu explicitamente que **todo nome novo** (pacotes/tipos Go, tabelas, colunas, views,
constraints) siga convenção de boas práticas **em inglês** — os nomes atuais em português
(`categorias`, `pessoas`, `emprestimos`, ...) são citados neste plano só como referência ao que
existe hoje, nunca como o nome final no código/banco novo (seção 9 formaliza a convenção e a
tabela de tradução completa). Também pediu boas práticas de **logging** pra nunca ficar às escuras
durante a execução (seção 12) e atenção à **complexidade das funções em notação Big O** (seção 13).

Depois de ler o plano completo, o usuário fez uma revisão de engenheiro e apontou 3 lacunas reais
antes de virar código (as duas primeiras foram sobre o mecanismo de tenant, hoje removido — ficam
registradas aqui só como histórico de por que o código passou pelas duas formas): (1) RLS +
`pgxpool` reaproveitando conexão exigia `SET LOCAL` por transação, não `SET` solto — isso deixou
de importar quando o tenant foi removido, mas o helper `db.WithTx` manteve o padrão de "toda query
passa por um wrapper central", só que sem `SET LOCAL`; (2) worker pool em memória perde jobs
`PROCESSING` num restart sem um mecanismo de lease — ainda válido, independente de tenant, seção 4;
(3) rodar Java+banco antigo e Go+banco novo em paralelo durante a transição é o maior risco
operacional do plano e precisa de uma decisão explícita — seção 14, também ainda válida. Ler
`lib/parentesco-grafo.ts` (frontend) pra investigar um ponto relacionado (a view
`v_person_relationships` não cobre a derivação de parentesco indireto) revelou que a coluna
`pessoas.parentesco` que a seção 10 mandava eliminar é usada hoje como uma das três fontes do
heurístico de gênero (`generoDeId`) — corrigido com uma coluna `people.gender` própria (seção
10.4); esse achado não depende de tenant e continua de pé.

Este plano foi validado lendo diretamente o código-fonte real (`categorias` module completo,
`TenantContextFilter`/`TenantContextHolder`, `JobRunnerService`, `GlobalExceptionHandler`,
`application-test.yml`, e os arquivos de migration citados abaixo: `V16`/`V36`/`V37` (permissões
booleanas em `tenant_members`), `V8`/`V49`/`V53` (padrão `_historico`), `V17`/`V35`/`V55`
(`categoria_pai_id`, `parentesco`, `pessoa_relacoes`)) — não apenas as specs em `.claude/`.

## Decisões de design

### 1. Layering
`handler.go` (HTTP puro, decodifica/valida/chama service/serializa) → `service.go` (regra de
negócio, define a interface `Repository` que consome — inversão de dependência idiomática em Go,
ao contrário do Java onde quem implementa é quem declara a interface) → `postgres/repository.go`
(único arquivo do módulo que importa driver SQL). Handler nunca acessa repositório; service nunca
vê tipos `net/http` — mesma regra do código atual.

### 2. Organização de módulos: por domínio (espelha o Java, nomes em inglês)
Cada módulo (`internal/categories/`, `internal/expenses/`, ...) reúne handler/service/domain/dto/
mapper como arquivos irmãos no mesmo pacote — só o adapter concreto de banco vai num subpacote
(`categories/postgres`) para isolar o import do driver SQL. `internal/` do Go dá encapsulamento
mais forte que package-private do Java (nada fora do módulo enxerga tipos não exportados),
reforçando em nível de compilador a regra "controller nunca acessa repositório direto". Nomes de
pacote seguem a convenção Go (minúsculo, sem underscore, uma palavra sempre que possível) — ver
tabela de tradução completa na seção 9.

**Nota de implementação (não estava no desenho original):** o `NewModule(db)` que a árvore de
diretórios originalmente sugeria dentro de `internal/categories/module.go` importando
`internal/categories/postgres` criaria um ciclo de import — `postgres` já precisa importar
`categories` (pelo tipo `Category`/interface `Repository`), então `categories` não pode importar
`postgres` de volta. A composição (montar `postgres.NewRepository` + `NewService` + `NewHandler`)
foi movida pra `cmd/api/main.go`, o único lugar que pode importar os dois sem ciclo; `categories`
expõe só `RegisterRoutes(mux, handler)` em `routes.go`. Aplicar o mesmo padrão nos outros 24
módulos.

### 3. Sem isolamento de tenant — decisão de produto, não lacuna técnica

**Esta seção substitui inteiramente o desenho original de isolamento multi-tenant.** A versão
anterior deste plano tratava isolamento de tenant como "o mecanismo mais crítico" do sistema,
espelhando o `@TenantId` do Hibernate com um `tenant.Scope` obrigatório em toda assinatura de
repositório + Row-Level Security no Postgres com `SET LOCAL app.tenant_id` por transação. Boa parte
disso chegou a ser implementada (`internal/platform/tenant/`, `db.WithTenantTx`, RLS na primeira
migration) e **verificada funcionando de verdade** contra um Postgres real antes de ser removida
(achado operacional do processo: RLS não se aplica ao dono da tabela/superusuário a menos que
`FORCE ROW LEVEL SECURITY` esteja setado; guardar esse aprendizado é útil mesmo sem tenant, caso
RLS volte a ser necessário por outro motivo no futuro).

O usuário esclareceu que esta reescrita não vai ser oferecida como SaaS pra múltiplas organizações
— é de uso pessoal/doméstico, com a possibilidade de convidar outras pessoas como usuários no
futuro, mas **sem separação de dados por organização**. Ou seja: pode existir mais de um
`app_users`, mas todos compartilham o mesmo conjunto de dados (`people`, `categories`, `loans`,
...) — não existe mais "tenant" nenhum, nem no banco nem no código.

**O que isso simplifica, concretamente:**
- Nenhuma tabela de domínio tem coluna `tenant_id`. Nenhuma tabela tem Row-Level Security. Nenhuma
  transação precisa de `SET LOCAL`.
- Nenhum método de repositório recebe `scope tenant.Scope` — só `(ctx context.Context, ...)`.
- `internal/platform/tenant/` (todo o pacote: `context.go`, `scope.go`, `middleware.go`) foi
  removido.
- `db.WithTenantTx` virou `db.WithTx(ctx, pool, func(tx pgx.Tx) error) error` — abre transação,
  roda o callback, comita/reverte. Sem `SET LOCAL`, sem tenant.
- `jobs.Job` perdeu o campo `TenantID`; jobs rodam com um `context.Context` comum, sem nenhum
  `tenant.WithTenant` — a lição de "montar o contexto antes de disparar a goroutine, nunca
  capturar variável mutável" (seção 4) continua válida por outros motivos (logger por job), só não
  carrega mais tenant.
- `tenants`, `tenant_members`, `member_permissions` saem do schema (seção 10 atualizada). No lugar,
  se/quando o usuário quiser convidar outras pessoas: `app_users(id, email, role)` como identidade,
  e opcionalmente `user_permissions(user_id, module, can_read, can_write)` — o mesmo formato
  normalizado que a seção 10 já propunha pras permissões, só sem a dimensão de tenant. Isso é
  **opcional e adiado** — nenhuma tabela de permissões existe na primeira migration; adicionar só
  quando um segundo usuário de verdade precisar de acesso restrito.
- Dados que hoje são "por tenant" (`nome_empresa`/`pix_key`/`logo_path` da tabela `tenants`, usados
  na marca d'água do PDF) viram um único registro de configuração global —
  `app_settings(id boolean PRIMARY KEY DEFAULT true CHECK (id), company_name, pix_key, logo_path,
  updated_at)` — o idioma de "tabela de uma linha só" (PK booleana que só aceita `true`) garante
  exatamente uma linha, sem precisar de um tenant pra ela pertencer.

### 4. Contexto de job assíncrono (sem tenant)
`JobRunnerService.processar` no Java é deliberadamente **não** `@Transactional`: o proxy
transacional abriria a Session do Hibernate (resolvendo o tenant) antes do corpo do método rodar
`TenantContextHolder.set` — por isso o `TenantContextHolder.set` roda primeiro, e a transação é
aberta manualmente via `TransactionTemplate` (confirmado lendo
`src/backend/.../relatorios/service/JobRunnerService.java:112-128`). Sem tenant, esse motivo
específico não se aplica mais no Go — mas a lição mais geral continua: **construir o contexto do
job (logger com `job_id`, etc.) antes de disparar a goroutine, nunca capturar uma variável
mutável/compartilhada**, exatamente como `internal/jobs/runner.go` já faz (`jobCtx :=
logging.WithLogger(context.Background(), logger.With(...)); go worker(jobCtx, job)`). Um worker
pool (channel + N goroutines) recebe `(ctx, job)` sem compartilhar estado entre jobs.

**Lacuna real do worker pool em memória — recuperação de crash via lease (continua válida, não
depende de tenant):** um worker pool via channel/goroutines perde todo job em andamento no
instante em que o processo reinicia (e os containers deste app reiniciam com frequência, sem
garantia de drain gracioso). Uma linha de `jobs` marcada `PROCESSING` no momento de um crash
ficaria travada pra sempre, já que nada a volta pra `PENDING`. Correção: toda linha pega pra
processar ganha um `locked_until timestamptz` (uma lease curta, ex. agora + 5 minutos, renovada se
o job legitimamente durar mais) setado na mesma transação que muda o status pra `PROCESSING`; a
query de polling do picker vira `WHERE status = 'PENDING' OR (status = 'PROCESSING' AND
locked_until < now())` — um job órfão cuja lease expirou (crash, OOM-kill, redeploy forçado) é
automaticamente reclamado pelo próximo tick do picker, sem intervenção humana.

### 5. Erros — RFC 7807 sem `@RestControllerAdvice`
Confirmado lendo `GlobalExceptionHandler.java`: cada tipo de exceção mapeia pra um status HTTP +
`type` URI estável (`https://pandora.finance/erros/{slug}` no Java atual), rejeições esperadas
logam WARN, falhas genuínas logam ERROR, catch-all cobre o resto. Equivalente em Go: um tipo
`apperr.Error` (`Kind`, `Type` slug, `Message`, `Fields`, `Err` encapsulado) com um construtor por
`Kind`, e um único decorator `httpx.Wrap(func(w,r) error) http.HandlerFunc` que mapeia
centralmente `*apperr.Error` → JSON `problem+json` + status — handlers retornam `error` em vez de
montar resposta manualmente. Erros de `sqlc`/`pgx` (`pgx.ErrNoRows`, violação de unicidade
`23505`) são traduzidos pra `apperr` na borda do repositório, espelhando
`handleDataIntegrity`/`handleNotFound`. O slug `Type` do erro nasce em inglês
(`resource-not-found`, `validation-failed`) sob o domínio `https://atlas.finance/errors/{slug}` —
já rebrandeado (ver nota de rebrand no topo), diferente do `pandora.finance` que o Java atual
ainda usa (fora de escopo mudar o Java por isso).

### 6. DTO / validação / mapping
Structs simples + tags `go-playground/validator` (análogo a `@NotBlank`) + funções de mapeamento
manuais (`toDTO`/`toDomain`, 5–15 linhas) — sem gerador de código tipo MapStruct. Justificativa:
para um app de 25 módulos, o custo de manter um toolchain de geração não compensa o boilerplate
mecânico que ele evitaria; revisitar só se um módulo específico tiver mapeamento genuinamente
complexo. DTOs (`dto.go`, com tag `json`) continuam separados de domínio (`domain.go`, sem tags),
preservando a mesma separação que o Java hoje faz via record vs. `@Entity`.

### 7. Injeção de dependência
Injeção via construtor, sem framework de DI — `func NewService(repo Repository) *Service`, cada
módulo expõe `RegisterRoutes(mux, handler)` (ver nota da seção 2 sobre por que a composição em si
— `postgres.NewRepository` → `NewService` → `NewHandler` — vive em `cmd/api/main.go`, não dentro
do próprio módulo). `cmd/api/main.go` conecta os 25 módulos explicitamente num único arquivo.
`google/wire`/`uber-go/fx` ficam só como nota de rodapé caso a fiação manual fique difícil de
manter.

### 8. Migrations e testes
- **Migrations:** `golang-migrate/migrate` (análogo direto do Flyway — sequencial, imutável). Como
  o banco novo tem schema redesenhado (seção 10), a numeração recomeça do zero
  (`000001_init_schema.up.sql`) num banco novo/separado — não é uma continuação do `V1..V56`
  atual. Ver seção 11 para como os dados existentes chegam nesse schema novo.
- **Testes — integration-first, espelhando a disciplina atual:** `TEST_DATABASE_URL` distinto de
  `DATABASE_URL` (mesmo motivo do `TEST_DB_URL` vs `DB_URL` hoje). Cada teste de integração abre
  transação e reverte no teardown (`defer tx.Rollback()`); construtor de repositório aceita uma
  interface `Querier` pequena satisfeita tanto por `*pgxpool.Pool` quanto por `pgx.Tx`, permitindo
  injetar a tx revertida nos testes. Testes de integração ficam atrás de build tag
  (`//go:build integration`) — `go test ./...` nunca toca Postgres por padrão. Testes unitários
  usam a stdlib `testing` + estilo table-driven.

### 9. Convenção de nomenclatura (obrigatória — código Go e banco novo, tudo em inglês)

**Regra geral:** todo identificador novo — pacote/tipo/função/variável Go, tabela, coluna,
constraint, índice, view — nasce em inglês. Nomes em português só aparecem neste plano como
referência ao que já existe no Java/schema atual, nunca como o nome definitivo.

**Go:**
- Pacotes: minúsculo, sem underscore, de preferência uma palavra só (convenção idiomática Go) —
  `categories`, `expenses`, `income`, `health`, `assets`, `documents`, `ai`, `integrations`,
  `notifications`, `people`, `personinfo`, `personrelations`, `finance`, `reports`, `networth`,
  `extraction`, `events`.
- Tipos exportados: `PascalCase` (`Category`, `CreateCategoryRequest`, `Repository`).
- Campos/variáveis locais: `camelCase`; siglas comuns em maiúsculo (`userID`, `httpServer`, não
  `userId`/`httpserver`).
- Métodos de service: verbo em inglês (`List`, `Create`, `Delete`, `SeedDefaults` — não
  `Listar`/`Criar`/`Remover`/`criarPadroesParaTenant` do Java atual; note que `SeedDefaults` não
  carrega mais "ParaTenant" — semear categorias padrão passa a ser uma operação global de
  primeiro-uso, não por tenant).

**Banco de dados:**
- Tabelas: substantivo no plural, `snake_case` minúsculo (`clients`, `loans`, `categories`,
  `installments`, não `cliente`/`emprestimo` no singular).
- Chave primária: sempre `id` (uuid) — exceto a tabela singleton `app_settings`, que usa uma PK
  booleana deliberadamente (seção 10) pra garantir exatamente uma linha.
- Chave estrangeira: sempre `<recurso_referenciado_no_singular>_id` (`client_id`, `category_id`);
  auto-relacionamento explícito no nome (`parent_category_id`, não `pai_id`).
- Colunas de enum-como-varchar validadas por `CHECK`: nome da coluna é o substantivo do domínio
  (`status`, `type`, `role`, `species`), valores em `UPPER_SNAKE_CASE` **em inglês**.
- Booleans: prefixo `is_`/`has_`/`can_` conforme semântica (`can_read`, `can_write`, não
  `permite_leitura`).
- Datas/horas: sempre `timestamptz`, sufixo `_at` (`created_at`, `updated_at`) — nunca `timestamp`
  sem timezone (ver seção 10.1).

**Tabela de tradução (nomes atuais em português → nomes novos em inglês, banco e módulo Go):**

| Português (atual) | Inglês (novo) | Onde aparece |
| --- | --- | --- |
| `categorias` | `categories` | tabela + pacote Go |
| `categoria_pai_id` | `parent_category_id` | coluna (self-FK) |
| `emprestimos` | `loans` | tabela + pacote Go |
| `parcelas` | `installments` | tabela |
| `movimentacoes` | `loan_transactions` | tabela |
| `saldo_devedor` | `outstanding_balance` | coluna |
| `taxa_juros_mensal` | `monthly_interest_rate` | coluna |
| `tipo_pagamento` | `payment_type` | coluna |
| `clientes` | `clients` | tabela |
| `pessoas` | `people` | tabela + pacote Go |
| `pessoa_relacoes` | `person_relationships` | tabela + view (`v_person_relationships`) |
| `pessoa_informacoes` | `person_information` | tabela |
| `pessoainformacoes`/`pessoarelacoes` (pacote Java) | `personinfo`/`personrelations` | pacote Go |
| `pessoa_informacoes_historico` | `audit_logs` (unificado) | tabela (seção 10.6) |
| `saldos_financeiros_historico` | `financial_balance_history` (mantida tipada, não vira `audit_logs`) | tabela (seção 10.6) |
| (nova, sem equivalente hoje) | `people.gender` | coluna (substitui heurístico de gênero, seção 10.4) |
| `bens` | `assets` | tabela + pacote Go |
| `documentos` | `documents` | tabela + pacote Go |
| `gastos` | `expenses` | tabela + pacote Go |
| `receitas` | `income_entries` | tabela; pacote Go `income` |
| `registros_saude` | `health_records` | tabela; pacote Go `health` |
| `marcador_catalogo` / `marcador_catalogo_id` | `health_marker_catalog` / `marker_catalog_id` | tabela / coluna |
| `pets`, `especie` | `pets`, `species` | tabela/coluna (já em inglês / traduzido) |
| `chat_mensagens`, `papel`, `ferramentas_chamadas` | `chat_messages`, `role`, `tool_calls` | tabela + colunas (pacote `ai`) |
| `ia_configuracoes`, `ordem_provedores` | `ai_configurations`, `provider_order` | tabela + coluna |
| `integracoes_externas`, `modelos` | `external_integrations`, `models` | tabela + coluna (pacote `integrations`) |
| `patrimonio`, `financeiro`, `relatorios`, `extracao`, `eventos`, `notificacoes` (pacotes Java) | `networth`, `finance`, `reports`, `extraction`, `events`, `notifications` | pacote Go |
| `tenants`, `tenant_members` (removidas, seção 3) | `app_users` (identidade), `app_settings` (config global singleton) | tabela nova, opcional/adiada |

**Valores de enum confirmados no código atual (traduzir 1:1 ao criar o `CHECK` novo):**
`EmprestimoStatus` (`ATIVO`/`INADIMPLENTE`/`QUITADO`/`CANCELADO` — confirmado em
`JobRunnerService.java`) → `ACTIVE`/`OVERDUE`/`PAID_OFF`/`CANCELLED`; `JobStatus`
(`PENDENTE`/`EM_PROCESSAMENTO`/`CONCLUIDO`/`FALHOU` — confirmado no mesmo arquivo) →
`PENDING`/`PROCESSING`/`COMPLETED`/`FAILED`; `CategoriaTipo` (`GASTO`/`RECEITA`/`BEM` — confirmado
no módulo `categorias`) → `EXPENSE`/`INCOME`/`ASSET`; `TipoMovimentacao`
(`DESEMBOLSO`/`RECEBIMENTO_PARCELA`/`RECEBIMENTO_AVULSO`/`PERDAO`/`AMORTIZACAO`) →
`DISBURSEMENT`/`INSTALLMENT_PAYMENT`/`ONE_OFF_PAYMENT`/`FORGIVENESS`/`AMORTIZATION`;
`pessoa_relacoes.tipo` (`MAE_DE`/`PAI_DE`/`CONJUGE_DE`, do levantamento do usuário) →
`MOTHER_OF`/`FATHER_OF`/`SPOUSE_OF`. Demais colunas enum-como-varchar (`bens.status`,
`parcelas.status`, `pets.especie`, `chat_mensagens.papel`) não tiveram os valores lidos linha a
linha neste plano — antes de escrever o `CHECK` de cada uma, rodar `SELECT DISTINCT` na coluna
correspondente do banco atual (mesmo passo de auditoria da seção 11) e traduzir a lista real, não
supor os valores.

### 10. Banco novo — schema redesenhado (banco à parte do `postgres-db` atual)

O banco atual está acima da média (índices compostos, partial indexes, FKs com
`ON DELETE CASCADE`), mas a reescrita Go é o momento certo pra corrigir os pontos abaixo de uma
vez, em vez de carregá-los pro banco novo. O banco novo nasce assim desde a primeira migration
(`000001_init_schema.up.sql`), já com todos os nomes traduzidos pela tabela da seção 9 — nenhum
destes é um `ALTER TABLE` incremental no banco antigo. **Sem tenant (seção 3): nenhuma tabela aqui
tem `tenant_id` nem Row-Level Security** — isso já é uma simplificação em relação ao desenho
original desta seção, não só uma tradução de nomes.

**Alto impacto:**
1. **`timestamptz` em toda coluna de tempo**, nunca `timestamp` sem timezone — evita o bug
   clássico de app assumindo `America/Sao_Paulo` e container/cloud rodando em UTC.
2. **`CHECK` constraint em toda coluna enum-como-varchar** (`person_relationships.type`,
   `loans.status`, `loans.payment_type`, `installments.status`, `assets.status`, `jobs.status`,
   `pets.species`, `chat_messages.role`, etc., com valores em inglês — ver tabela da seção 9) —
   `CHECK` em vez de enum nativo do PG: dá a maior parte do benefício de validação e é trivial de
   evoluir (`ALTER TABLE ... DROP CONSTRAINT` + `ADD CONSTRAINT` sem reescrever tipo).
3. **Índice em toda coluna de FK filha**, incluindo o self-FK `categories.parent_category_id` —
   hoje (nomes atuais) `bens.pessoa_id`/`categoria_id`, `documentos.pessoa_id`/`bem_id`/`pet_id`,
   `gastos.categoria_id`/`documento_id`, `receitas.documento_id`/`categoria_id`/`pessoa_id`,
   `registros_saude.marcador_catalogo_id`/`documento_id` não têm índice — sem isso, deletar uma
   pessoa (que dispara `CASCADE`) faz sequential scan na tabela filha inteira. No schema novo,
   todo `*_id` de FK recebe índice por padrão.

**Normalização:**
4. **`people.parentesco` deixa de existir como coluna própria** — hoje coexiste com o grafo
   `pessoa_relacoes` como fonte redundante (raiz de inconsistências já corrigidas). No banco novo,
   o vínculo relativo a "eu mesmo" (`SELF`) é 100% derivado do grafo `person_relationships`.
   **Achado ao ler `lib/parentesco-grafo.ts` (frontend) pra validar essa remoção:** a derivação
   real não é um join simples — é uma BFS (`caminhosDe`) sobre 4 tipos de aresta (sobe pra
   mãe/pai, desce pra filho, lado pro cônjuge), normalizada numa sequência de categorias
   (SOBE/DESCE/LADO) e casada contra uma tabela de ~19 padrões de caminho pra chegar no termo
   (`termoPorCaminho`), dependente do **gênero do alvo** — e `Pessoa` não tem campo de gênero
   hoje. `generoDeId` (linhas 118-124) infere gênero em cascata: (a) já apareceu como mãe/pai numa
   aresta; (b) senão, o **`pessoa.parentesco` guardado** já denuncia gênero (ex.: já era `IRMA`);
   (c) senão, masculino por padrão. Ou seja, a coluna que a seção 9/este item mandava eliminar é
   uma das três fontes do heurístico de gênero — removê-la sem substituto regride a inferência
   pra quem não tem aresta mãe/pai cadastrada. **Correção:** o banco novo ganha uma coluna própria
   `people.gender` (`CHECK IN ('M','F')`, nullable) em vez de inferir — mais correto que o
   heurístico em cascata, e faz a remoção de `parentesco` segura. Ao migrar (seção 11), preencher
   `gender` a partir do mesmo heurístico de `generoDeId` como *dado inicial* (não como mecanismo
   permanente).
   **A lógica de derivação em si (BFS + tabela de padrões) migra pro backend Go, não vira view
   SQL:** o "casar sequência de categorias contra ~19 padrões, dependente de gênero" é um mau
   encaixe pra SQL (viraria um `CASE` gigante sobre string concatenada); é uma tradução quase
   direta de `parentesco-grafo.ts` pra uma função Go pura em `internal/personrelations`
   (`DeriveFromOrigin(originID) map[uuid.UUID]DerivedRelationship`, espelhando `derivarDeOrigem`),
   operando sobre o grafo já carregado em memória (poucas centenas de nós no máximo, custo
   desprezível). Isso resolve a ambiguidade que a revisão do usuário levantou: o backend Go vira a
   fonte de verdade da derivação, o frontend passa a consumir um endpoint em vez de recalcular a
   BFS localmente — `parentesco-grafo.ts` é aposentado depois que o endpoint existir, não mantido
   em paralelo.
5. **Sem `tenant_members`/`member_permissions` (removidas com o tenant, seção 3).** Se/quando o
   usuário quiser convidar outro usuário com acesso restrito, a mesma ideia de normalizar
   permissões numa tabela em vez de N colunas booleanas continua valendo — só sem a dimensão de
   tenant: `user_permissions(user_id, module, can_read, can_write)`, `module` usando os nomes em
   inglês da seção 9. **Não faz parte da primeira migration** — só adicionar quando for
   necessário de verdade.
6. **Padrão `_historico` — unificar só o que é auditoria, não o que é feature de produto.**
   `pessoa_informacoes_historico` (trilha de edição, ninguém monta gráfico em cima) vira
   `audit_logs(table_name, row_id, old_data jsonb, changed_at)` genérico, populado por trigger.
   `saldos_financeiros_historico` **não** — é consumido como série temporal (gráfico de saldo ao
   longo do tempo no dashboard), e um `jsonb` por linha é mais lento e pior de indexar/agregar do
   que colunas tipadas pra esse uso; continua como tabela tipada dedicada
   (`financial_balance_history(client_id, balance, recorded_at)`, sem o campo `jsonb` genérico e
   sem `tenant_id`). Regra geral pra aplicar a cada `_historico` legado ao portar cada módulo: se é
   só trilha de auditoria/compliance, unifica em `audit_logs`; se é dado que vira gráfico/feature
   na UI, mantém tabela tipada própria.
7. **`app_settings` — configuração global única**, substituindo os campos por-tenant de
   `nome_empresa`/`pix_key`/`logo_path` que hoje vivem em `tenants` (usados na marca d'água do
   PDF): `app_settings(id boolean PRIMARY KEY DEFAULT true CHECK (id), company_name, pix_key,
   logo_path, updated_at)`. A PK booleana que só aceita `true` é o idioma padrão de "tabela de uma
   linha só" — garante que nunca existam duas linhas de configuração concorrentes, sem precisar de
   um tenant pra cada uma pertencer.

**Views:**
8. `v_person_relationships` — materializa só as arestas primárias do grafo (`person_relationships`
   + join em `people` pros nomes de origem/destino), pra consumo direto do backend Go. A derivação
   de relações indiretas (irmão, avô, tio, cunhado, ...) **não** vive nesta view — ver a correção
   do item 4 acima: é uma função Go em `personrelations`, não SQL.
9. `v_client_loan_balance` — empréstimos ativos + saldo devedor agregado por cliente, pro
   dashboard.

**Detalhes finos:**
10. `loans.monthly_interest_rate`: `numeric(5,2)` → `numeric(12,6)` (não `numeric(6,3)` — juros
    compostos mensais alimentam cálculos intermediários de taxa efetiva diária/fracionária, e 3
    casas decimais arredondam demais nesses passos intermediários; `(12,6)` é o padrão conservador
    pra campos de taxa financeira e não custa nada a mais).
11. `chat_messages.tool_calls` (hoje `ferramentas_chamadas`, `text` com JSON serializado como
    string) → `jsonb`.
12. `ai_configurations.provider_order` e `external_integrations.models` (hoje `varchar` com lista
    separada por vírgula) → `jsonb` ou `text[]`.

### 11. Migração do banco antigo (`postgres-db`/produção homelab) pro banco novo

> **Histórico (revisado 2026-08-28).** O placeholder `cmd/migrate-legacy` foi removido; ver
> `docs/legacy-etl-retirement.md`. Esta seção permanece como registro da estratégia caso uma
> importação real seja retomada — que deve nascer como tarefa própria, não ressuscitando o binário.

Como o schema é redesenhado (não é um `ALTER TABLE` no banco existente), a migração de dados é um
**ETL de corte único** (offline, dentro de uma janela de manutenção), não uma réplica contínua —
adequado à escala/operação atual (self-hosted homelab), não uma migração zero-downtime em escala
de nuvem. Sem tenant no banco novo, a carga de `tenants`/`tenant_members` deixa de existir — o
banco antigo continua tendo essas tabelas (é multi-tenant de verdade), mas o ETL simplesmente não
migra a dimensão de tenant: os dados de todos os tenants antigos convergem pro único conjunto de
dados do banco novo (aceitável porque, na prática, é uso pessoal — não há hoje múltiplos tenants
reais de produção com dados que precisem continuar separados; se isso não for verdade, esta seção
precisa ser revisitada antes do corte).

- **Ferramenta:** um binário Go dedicado (`cmd/migrate-legacy/main.go`, fora do fluxo normal da
  API) que abre uma conexão **somente-leitura** no `postgres-db` atual e escreve no banco novo —
  nunca a aplicação principal apontando pros dois bancos ao mesmo tempo (mesmo espírito da regra
  já existente de nunca misturar `DB_URL` de produção com o datasource de teste).
- **Ordem de carga** (nome de destino já traduzido pela tabela da seção 9): `app_settings`
  (populado a partir da linha `tenants` relevante) → `clients`/`people`/`categories` (resolvendo o
  self-FK `parent_category_id` em duas passadas: insere tudo sem pai, depois faz `UPDATE` do
  `parent_category_id`) →
  `loans`/`installments`/`loan_transactions`/`expenses`/`income_entries`/`assets`/`documents`/
  `pets`/... → `person_relationships` (com backfill: qualquer `parentesco` antigo que não tenha
  uma aresta correspondente em `pessoa_relacoes` vira uma aresta antes do corte, pra não perder
  dado na eliminação da coluna) → `jobs` (só os `PENDING`/`PROCESSING`, os concluídos antigos podem
  virar `audit_logs`/arquivo morto em vez de migrar linha a linha).
- **Backfill de `people.gender` (seção 10.4):** calculado durante a carga de `people` reaplicando
  o mesmo heurístico de `generoDeId` (`lib/parentesco-grafo.ts:118-124`) sobre o grafo já
  carregado — apareceu como mãe/pai numa aresta → `F`/`M`; senão, `parentesco` guardado denuncia
  gênero → idem; senão, `NULL` em vez do "masculino por padrão" do heurístico em tempo real (o
  backfill é uma correção de dado único, não deve *inventar* gênero pra quem não tem nenhum sinal
  — melhor `NULL` revisável manualmente do que um "M" errado silencioso).
- **Transformações obrigatórias durante a carga** (não depois):
  - `timestamp` → `timestamptz`, assumindo `America/Sao_Paulo` (`AT TIME ZONE
    'America/Sao_Paulo'`) pra todo `created_at`/`updated_at`/data de negócio.
  - Tradução de valor de enum: todo `varchar` de status/tipo/papel do banco antigo (em português,
    ex. `'ATIVO'`) é reescrito pro valor em inglês do `CHECK` novo (ex. `'ACTIVE'`) durante o
    `INSERT`, usando a tabela de valores confirmados da seção 9 como base e um `SELECT DISTINCT`
    de auditoria pra qualquer coluna cujos valores não estejam nessa tabela.
  - Validar que todo valor existente de cada coluna enum-como-varchar tem uma tradução definida
    **antes** de aplicar o `CHECK` novo — tratar qualquer valor sem tradução mapeada como
    bloqueador do corte, não como algo pra silenciosamente descartar.
  - `pessoa_informacoes_historico` → linhas em `audit_logs`; `saldos_financeiros_historico` →
    linhas em `financial_balance_history` (tabela tipada, mantém as colunas próprias — seção
    10.6 — não vira `jsonb`, e não carrega `tenant_id`).
  - CSV em `ia_configuracoes.ordem_provedores`/`integracoes_externas.modelos` → `jsonb`/`text[]`
    em `ai_configurations.provider_order`/`external_integrations.models`.
- **Validação pós-carga:** contagem de linhas por tabela (origem vs. destino), conferência de que
  todo valor de enum migrado bate com a tradução esperada (nenhum `NULL`/valor fora da lista do
  `CHECK`), e uma amostra manual dos registros migrados conferindo saldo devedor/relacionamentos
  antes de considerar o corte concluído. Manter o `postgres-db` atual intacto (somente leitura,
  não apagar) por um período de retenção após o corte, como rollback de última instância.

### 12. Logging — para nunca ficar no escuro em execução

Hoje o único lugar que padroniza log é `GlobalExceptionHandler`: rejeições esperadas (não
encontrado, validação, rate limit, acesso negado, corpo/parâmetro inválido) logam em **WARN**
(existem, mas antes eram totalmente silenciosas — o comentário do próprio arquivo, linhas 33-39,
descreve isso como o que tornava um bug real difícil de rastrear, "sem log nenhum ligando 'fulano
tentou entrar, foi rejeitado, aqui está o motivo'"), e o catch-all genérico loga em **ERROR**
(`handleGeneric`, linha 174-178) porque representa falha inesperada de verdade. `JobRunnerService`
já loga início de falha (`log.error("Falha ao processar job {}", jobId, e)`, linha 160) e job
não encontrado (`log.warn`, linha 141). O plano Go replica exatamente essa disciplina, não inventa
uma nova:

- **Logger estruturado, não `fmt.Println`/`log.Print`:** `log/slog` da stdlib (Go 1.21+, sem
  dependência externa) — cada linha de log é um evento com campos, não uma string livre.
- **Correlação por requisição:** o middleware de access log gera um `request_id` (uuid) e cria um
  logger-filho via `logger.With("request_id", id)`, guardado no `context.Context` — um helper
  `logging.FromContext(ctx)` recupera esse logger-filho em qualquer handler/service que precise
  logar algo ad hoc, sem precisar passar um `*slog.Logger` como parâmetro extra em toda assinatura.
  (Antes da remoção do tenant, esse logger também carregava `tenant_id` — removido junto.)
- **Nível de log = mesma regra do `GlobalExceptionHandler`:** o `apperr.Kind` de cada erro (seção
  5) decide o nível — `KindNotFound`/`KindValidation`/`KindRateLimited`/`KindForbidden` logam
  **WARN** (rejeição de regra de negócio esperada) dentro do próprio `httpx.Wrap(...)` central,
  antes de responder; qualquer erro que não seja `*apperr.Error` (o catch-all) loga **ERROR** com
  stack/causa completa — replicando `handleGeneric`. Nenhum handler individual decide isso por
  conta própria.
- **Toda requisição HTTP loga início/fim** (método, path, status, duração, `request_id`) via o
  mesmo middleware — equivalente ao access log que hoje só existe implicitamente via
  container/proxy.
- **Todo job assíncrono loga o ciclo de vida completo** (dispatch, início de processamento,
  conclusão, cada retry com o número da tentativa, falha final) — replicando o padrão já usado em
  `JobRunnerService.processarNaTransacao`, mas em toda transição de `JobStatus`, não só na falha.
- **Nunca engolir erro silenciosamente:** todo `if err != nil` que não propaga o erro (`return
  nil, err`/`return err`) precisa logar antes de descartar — nunca um `_ = doSomething()` que joga
  fora um erro de I/O/DB sem deixar rastro.

### 13. Reduzindo complexidade das funções — Big O explícito

**Regra geral:** toda função com laço aninhado sobre uma coleção de tamanho não trivialmente
limitado ganha um comentário de uma linha com a complexidade (`// O(n) round-trips, n =
len(loans)`) e, sempre que possível, é reescrita pra eliminar o laço em vez de só documentá-lo.

- **Evitar N+1 de banco — exemplo real já confirmado no código atual:**
  `JobRunnerService.processarExtratoPdf` (linhas 229-238) itera `emprestimosDoCliente` e, dentro
  do laço, chama `parcelaRepository.findAllByEmprestimoIdOrderByNumeroParcelaAsc(e.getId())` **uma
  vez por empréstimo** — isso é O(n) round-trips ao banco pra n empréstimos de um cliente. No
  módulo Go equivalente (`reports`, quando for portado), essa mesma extração de extrato vira **uma
  única query batched** (`WHERE loan_id = ANY($1)`) seguida de um agrupamento em memória
  (`map[uuid.UUID][]Installment`, O(n) sem round-trip extra) — reduz de O(n) chamadas de rede pro
  banco pra O(1), mantendo o mesmo O(n) de trabalho em memória. Esse é o padrão a seguir sempre que
  uma coleção pai precisar dos filhos de cada item: buscar tudo de uma vez com `ANY($1)`/`IN`,
  nunca um laço com uma query por iteração.
- **Repositórios (seção 3) são O(1) round-trip por chamada, por design:** cada método da interface
  `Repository` representa exatamente uma ida ao banco; um service que precisa combinar dados de
  duas tabelas usa `JOIN`/uma query com `ANY($1)`, não duas chamadas de repositório em série dentro
  de um laço (o mesmo erro do ponto acima, generalizado).
- **Complexidade ciclomática, não só Big O assintótico:** funções grandes com muitos
  `if`/`switch` aninhados (o `switch job.getTipo()` de `JobRunnerService.processarNaTransacao`,
  linhas 152-156, é um bom exemplo do tamanho certo — um `switch` raso, um caso por linha,
  delegando pra uma função por caso) viram o modelo a seguir: extrair cada ramo em uma função
  nomeada (`processExtratoPDF`, `exportSpreadsheet`, ...) em vez de um método longo com lógica
  inline nos dois braços.
- **Gate de lint:** `Makefile lint` roda `gocyclo` (ou `golangci-lint` com o linter `cyclop`
  habilitado) com um limite baixo (ex. complexidade ciclomática ≤ 10 por função) — falha o build
  antes de a função crescer demais, em vez de depender de review manual pra notar.
- **`categories` como baseline de referência:** `List`/`Create`/`Delete` do módulo de referência
  são O(1) round-trip de banco e O(log n)/O(1) de lookup por índice cada — nenhum laço, nenhuma
  complexidade além do necessário. Ao portar os outros 24 módulos, qualquer método que fugir desse
  baseline (mais de um round-trip por chamada de service, ou um laço com I/O dentro) deve
  justificar por quê no comentário de complexidade acima, não só ser deixado como está.

### 14. Convivência dos dois bancos durante a transição — decisão explícita de corte único

Enquanto o Go não cobrir os 25 módulos, existe uma janela onde o Java aponta pro banco antigo e o
Go (parcialmente pronto) apontaria pro banco novo redesenhado — dois bancos vivos ao mesmo tempo é
o risco operacional de maior impacto deste plano se ficar implícito, porque as duas opções óbvias
têm custos bem diferentes:

- **Rodar os dois backends em paralelo** (Java lendo o banco antigo, Go lendo o banco novo,
  módulo por módulo migrando) exigiria sincronização bidirecional contínua entre os dois schemas
  — não um ETL de corte único (seção 11), mas uma réplica ao vivo com toda a complexidade de
  resolução de conflito e decidir qual banco é a fonte de verdade por módulo. Esse custo só se
  paga numa operação que não pode ter downtime — não é o caso aqui.
- **Corte único (recomendado):** dado que a produção roda inteira num homelab self-hosted, com um
  operador só e sem SLA de disponibilidade contínua, uma janela de manutenção agendada é aceitável
  e muito mais simples: **o corte só acontece quando os 25 módulos estiverem portados e validados
  no Go**, não incrementalmente módulo a módulo contra o banco antigo. Até lá, o Java + banco
  antigo continuam sendo a única fonte de verdade em produção; o Go + banco novo evoluem isolados
  (banco de desenvolvimento próprio, sem tráfego real) até o dia do corte.

**Runbook do corte (quando os 25 módulos estiverem prontos):**
1. Congelar escrita no Java em produção (`docker compose stop` do serviço `backend` no
   `docker-compose.homelab.yml`, deixando o Postgres atual de pé, só leitura).
2. Rodar `cmd/migrate-legacy` (seção 11) contra o banco antigo real, escrevendo no banco novo.
3. Rodar a validação pós-carga completa da seção 11 (contagens, tradução de enum, amostra manual).
4. Subir o novo serviço Go apontando pro banco novo; só depois disso o Java para de vez.
5. Manter o `postgres-db` atual **intacto, somente leitura**, por um período de retenção (a seção
   11 já recomenda isso) — é o rollback de última instância se algo passar despercebido na
   validação.

Esse runbook assume o corte como um evento único, não uma migração módulo a módulo em produção —
se essa premissa mudar (ex.: pressão real por reduzir a janela de manutenção antes dos 25 módulos
estarem prontos), a decisão acima precisa ser revisitada explicitamente, não assumida por default.

## Árvore de diretórios (esqueleto + módulo de referência `categories`)

`categorias` (Java) é o módulo mais simples e completo hoje (8 arquivos, todas as camadas) —
recomenda-se portá-lo primeiro como referência, já com todos os identificadores em inglês
(`categories` no Go e no banco, ver seção 9) e sem tenant (seção 3).

```
atlas-go/
├── cmd/
│   ├── api/main.go                 # entrypoint HTTP: config, pool DB, composição de cada módulo (repo+service+handler), monta router, shutdown gracioso
│   ├── worker/main.go              # entrypoint do job-runner (equivalente a JobRunnerService+JobPickerScheduler) — stub até existir um módulo que produza jobs
│   └── migrate-legacy/main.go       # ETL de corte único: lê postgres-db atual (somente-leitura), escreve no banco novo redesenhado (seção 11) — stub até existir um 2º módulo baseline
├── internal/
│   ├── app/router.go                # monta a cadeia de middleware global (Recover, AccessLog) ao redor do mux que cada módulo populou via RegisterRoutes
│   ├── config/config.go             # carga de env vars (DATABASE_URL, TEST_DATABASE_URL, PORT, ...)
│   ├── platform/
│   │   ├── httpx/decode.go          # DecodeAndValidate[T] — decode JSON + validator.Struct, ponto único pra todo handler
│   │   ├── httpx/respond.go         # helpers de resposta JSON/problem+json (WriteJSON, WriteProblem)
│   │   ├── httpx/middleware.go      # Wrap(fn) central de erro (loga WARN/ERROR conforme apperr.Kind), AccessLog (método/path/status/duração/request_id), Recover (panic → apperr.Internal)
│   │   ├── apperr/error.go          # Error/Kind, construtores, tabela Kind→status HTTP, slugs em inglês sob https://atlas.finance/errors/{slug}
│   │   ├── logging/logger.go        # slog.Logger base (JSON), WithLogger/FromContext (seção 12)
│   │   ├── db/pool.go               # construção do pgxpool
│   │   ├── db/querier.go            # interface Querier satisfeita por *pgxpool.Pool e pgx.Tx (permite tx nos testes)
│   │   └── db/tx.go                 # WithTx: abre transação, roda callback, comita/reverte — sem SET LOCAL/RLS (seção 3, sem tenant)
│   ├── migrations/000001_init_schema.up.sql  # primeira migration golang-migrate, schema novo já com nomenclatura da seção 9, sem tenant_id/RLS
│   ├── jobs/
│   │   ├── repository.go            # Job (sem TenantID), Repository interface; lease locked_until (seção 4)
│   │   ├── runner.go                # worker pool: channel de Job, N goroutines, contexto+logger por job (sem tenant)
│   │   └── picker.go                 # loop de polling via ticker (equivalente a JobPickerScheduler), reclama PROCESSING com lease expirada
│   └── categories/                   # <- módulo de referência, portado primeiro (categorias → categories, seção 9)
│       ├── domain.go                 # struct Category (ID, Name, ParentCategoryID *uuid.UUID, Type *CategoryType, CreatedAt) — sem TenantID; enum CategoryType
│       ├── dto.go                    # CategoryDTO, CreateCategoryRequest (tags json + validate)
│       ├── mapper.go                 # toDTO(Category) / toDTOs([]Category)
│       ├── service.go                 # interface Repository (definida pelo consumidor) + List/Create/Delete/SeedDefaults, todo método recebe (ctx, ...) — sem scope
│       ├── handler.go                 # handlers List/Create/Delete — espelha os 3 endpoints de CategoriaController
│       ├── routes.go                  # RegisterRoutes(mux, handler) — ver nota da seção 2 sobre por que a composição real vive em cmd/api, não aqui
│       ├── postgres/repository.go     # implementação concreta de Repository; usa db.WithTx (sem tenant)
│       ├── category_test.go                # testes unitários: mapper + lógica de service contra um Repository fake em memória, table-driven
│       └── category_integration_test.go    # //go:build integration — Postgres real, tx revertida, exercita Handler→Service→postgres.Repository
├── go.mod / go.sum
├── Makefile                          # targets migrate up/down, testes unitário vs integração, lint (inclui gocyclo/cyclop, seção 13)
└── docker-compose.test.yml           # Postgres efêmero pra rodar integração local (ou testcontainers-go)
```

## Arquivos Java/SQL de referência (no repo `pandora-loan-helper`)

**Arquitetura (consultar ao portar `categorias`→`categories` de verdade):**
- `src/backend/src/main/java/br/com/atlas/categorias/service/CategoriaService.java`
- `src/backend/src/main/java/br/com/atlas/categorias/repository/CategoriaRepository.java`
- `src/backend/src/main/java/br/com/atlas/relatorios/service/JobRunnerService.java`
- `src/backend/src/main/java/br/com/atlas/common/GlobalExceptionHandler.java`
- `src/backend/src/test/resources/application-test.yml`

**Schema atual (consultar ao desenhar o schema novo e o ETL da seção 11 — traduzir todo nome
usando a tabela da seção 9; note que o banco atual é multi-tenant, o novo não é — seção 3):**
- `src/backend/src/main/resources/db/migration/V8__historico_import_support.sql`,
  `V49__pessoa_informacao_historico.sql`, `V53__saldo_financeiro_historico.sql` (padrão
  `_historico` a unificar em `audit_logs`)
- `src/backend/src/main/resources/db/migration/V17__categoria_pai_fk_deferrable.sql`,
  `V35__normaliza_parentesco_pessoa.sql`, `V55__pessoa_relacoes.sql` (self-FK de categoria →
  `parent_category_id`, `parentesco` vs. grafo → `person_relationships`)

**Frontend (consultar ao portar a derivação de parentesco pro módulo `personrelations` em Go,
seção 10.4), no repo `pandora-loan-helper-frontend`:**
- `lib/parentesco-grafo.ts` — BFS (`caminhosDe`), heurístico de gênero (`generoDeId`), tabela de
  padrões de caminho → termo (`termoPorCaminho`), ponto de entrada `derivarDeOrigem`. A tradução Go
  (`personrelations.DeriveFromOrigin`) deve seguir essa estrutura de perto — não é um recálculo do
  zero, é uma porta 1:1 da mesma lógica.

## Verificação

1. Criar `atlas-go/` como novo repo/diretório (não dentro de `src/backend`), inicializar `go.mod`.
2. Implementar o esqueleto de `internal/platform/` (apperr, httpx, db, logging) primeiro — são a
   base que todo módulo de domínio vai depender.
3. Escrever a primeira migration do banco novo (`000001_init_schema.up.sql`) já incorporando os
   pontos da seção 10 (timestamptz, CHECK, índices de FK, `audit_logs`, `app_settings`, views,
   sem tenant_id/RLS) e a nomenclatura em inglês da seção 9, e subir um Postgres local só com esse
   schema pra validar que ele cria sem erro.
4. Portar `categories` como módulo de referência, com `go build ./...`, `go test ./...` (unit) e
   `go test -tags=integration ./...` (contra Postgres real de teste) passando.
5. **Antes de declarar `categories` pronto como template pra todos os outros 24:** portar um
   segundo módulo mais denso financeiramente (`loans`, com juros/amortização/parcelas) como
   segundo baseline. `categories` valida a arquitetura (layering, erros, DTO/mapping); não valida
   lidar com a complexidade de domínio real que a maioria dos outros módulos vai ter — só depois
   de dois baselines (um simples, um financeiro) usar o template pros 22 restantes.
6. Escrever `cmd/migrate-legacy` e testar o ETL contra uma cópia do `postgres-db` atual (nunca a
   instância real de produção) antes de qualquer corte de verdade — conferindo contagens de linha,
   tradução de valores de enum, backfill de `people.gender` e uma amostra dos registros migrados,
   conforme seção 11.
7. Ao portar `categories`, confirmar que toda requisição HTTP e todo evento de job aparecem no log
   estruturado com `request_id` (seção 12), e rodar `gocyclo`/`golangci-lint` (seção 13) como
   parte do `make lint` antes de considerar o módulo de referência pronto pra virar template dos
   outros 23.
8. Só marcar a estratégia de corte da seção 14 como "pronta pra executar" depois que os 25 módulos
   (23 + `categories` + `loans`) estiverem portados e validados — corte único, não incremental
   contra produção, conforme decisão da seção 14.
