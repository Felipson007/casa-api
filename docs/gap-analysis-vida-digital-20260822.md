# Gap analysis — "Atlas como SO da vida" (2026-08-22)

> **Status: histórico; revisado em 2026-08-28.** Diversos gaps abaixo foram implementados após a
> data original. Antes de criar tarefa, confronte este documento com o código e com o backlog atual
> do frontend em `.claude/roadmap/05-backlog-current.md`. Assinaturas e ETL possuem decisões atuais
> em `docs/subscriptions-source-of-truth.md` e `docs/legacy-etl-retirement.md`.

## Contexto

O usuário recebeu de outra IA uma proposta de arquitetura ("Atlas como sistema operacional da
vida": índice de identidade/finanças/patrimônio/saúde/contratos, inventário de contas digitais,
central de assinaturas, inbox com IA, mapa da vida, kit de emergência etc.) e pediu uma comparação
honesta contra o `atlas-go` real. A investigação (lendo `internal/*`, `docs/architecture-plan.md`,
`decisions/DECISIONS.md`, `app-frontend-atlas/components/*`) mostrou que boa parte da visão já
existe e está rodando em produção — `people`/`personrelations` (família + grafo de parentesco),
`personinfo` (identidade/carreira/educação), `assets` (patrimônio, com crawler de valor estimado),
`health`, `pets`, `documents` (com extração por IA para 3 origens), `expenses`/`income`/`loans`,
`notifications`, `backup`, `chat`/`assistant` (IA com tool-calling e confirmação antes de escrever).

Este documento detalha os **gaps reais** identificados nessa comparação e quebra cada um em tarefas
concretas. Não segue o formato `decisions/DECISIONS.md` (D-0XX, exige acordo Hermes×Claude — esse
protocolo era da fase de reescrita, hoje encerrada) nem recria `TASKS.md` (removido deliberadamente
em `cceec4a` quando a reescrita terminou) — é um documento de planejamento único, para o usuário
priorizar e (se/quando decidir) transformar em trabalho real, por si ou por um agente.

## Como ler isto

Cada gap tem: **o que falta**, **por que importa**, **o que já existe para construir em cima** (para
não redesenhar do zero), e uma lista de tarefas com prioridade (P0 = maior alavancagem/menor custo,
P2 = nice-to-have) e esforço estimado (S/M/L, assumindo o padrão já usado no resto do repo:
handler→service→postgres.Repository, DTO+mapper, teste de integração com `testdata/seed.sql`).

## Status de implementação (atualizado 2026-08-22, sessão 2)

O conceito visual do "Mapa da Vida" (dashboard alvo, cores/tokens reais do `app-frontend-atlas`)
foi desenhado como canvas editável: https://claude.ai/code/artifact/d13c014b-a7dc-4a7e-b1cc-cefc18182ee2
— referência de UI pra GAP 6 (T6.2) e pros itens de navegação de GAP 1/3/4/7 quando a etapa de
frontend de cada um chegar.

Trabalho de schema/backend já iniciado nesta sessão, seguindo o sequenciamento da seção final deste
documento (GAP 2 → GAP 3 → GAP 1 em paralelo):

- **GAP 2 — `documents` enriquecido: ✅ feito de ponta a ponta.** Migration
  `000008_documents_metadata.up/down.sql` aplicada ao schema (não rodada contra nenhum banco ainda —
  ver "Próximos passos" abaixo). `Document`/`DocumentDTO`/`CreateDocumentRequest`/
  `UpdateDocumentRequest`/`mapper.go`/`postgres/repository.go` (SELECT/INSERT/UPDATE/scan) todos
  atualizados com os 6 campos novos. `expiration_date` é `date` (não `timestamptz`) — mesma
  convenção do `document_date` existente (é uma data de calendário, não um instante com fuso).
- **GAP 3 — `Origin` estendido: ✅ feito.** `OriginInsurance`/`OriginWarranty`/`OriginTax`
  adicionados em `domain.go`, tag `oneof` do `dto.go` atualizada, e `storage.go` ganhou subpastas
  próprias (`Seguros`/`Garantias`/`Fiscal`) em vez de cair em "Outros".
- **GAP 1 — `digital_accounts`: ✅ feito (T1.1–T1.4).** Migration
  `000009_digital_accounts.up/down.sql` (tabela completa, sem coluna de senha por design — ver
  comentário no `.up.sql`) + módulo `internal/digitalaccounts` completo (domain/dto/mapper/service/
  handler/routes/postgres, espelhando `internal/assets`), incluindo o filtro `?email=` (T1.3) e
  `POST /api/digital-accounts/{id}/mark-reviewed` (T1.4). Wired em `cmd/api/main.go`.
  T1.5 (frontend) e T1.6 (ferramenta do assistant) seguem pendentes.
- **GAP 4 — `subscriptions`: 🟡 schema pronto, módulo Go pendente.** Migration
  `000010_subscriptions.up/down.sql` criada. Módulo `internal/subscriptions` (T4.2–T4.5) é o
  próximo passo — **usar `internal/digitalaccounts` como template direto** (acabou de ser escrito
  com a mesma forma), adaptando: sem `MarkReviewed`, mas com um endpoint de soma
  (`GET /api/subscriptions/summary`) e o filtro/índice por `next_renewal_date` em vez de `email`.
- **Verificado com build real**: `docker run --rm -v "...atlas-go:/app" -w /app golang:1.25 sh -c
  "go build ./... && go vet ./... && gofmt -l ."` — limpo (0 erros, 0 arquivos mal formatados) via
  PowerShell (não Bash — MSYS mangla o path `-w /app`). **`go.mod` exige Go ≥1.25**, não 1.22 como
  `docs/agent-collab.md` registra (documento desatualizado nesse ponto, não corrigido aqui).
- **GAP 5/6/7/8**: inalterados, dependem do que está pendente acima (ver notas de dependência em
  cada seção).

**Correção de numeração:** as tarefas T1.1/T2.1 abaixo, escritas na sessão anterior, citavam
`000008` para GAP 1 e `000009` para GAP 2 — na hora de escrever de verdade, GAP 2 saiu primeiro
(é a dependência de tudo mais) e ficou com `000008`; GAP 1 ficou com `000009`. O texto de T1.1/T2.1
abaixo mantém os números antigos como estavam escritos — os arquivos reais são
`000008_documents_metadata` (GAP 2) e `000009_digital_accounts` (GAP 1).

**Próximos passos concretos, nessa ordem:**
1. Rodar as 3 migrations novas (`000008`/`000009`/`000010`) contra um Postgres real
   (`atlas-test-db`/dev) — ainda não aplicadas em nenhum banco, só escritas e com o build Go
   verificado contra o código (não contra o schema de verdade). `golang-migrate` já sabe achar
   `internal/migrations/*` — não foi rodado nesta sessão (sem Postgres local disponível).
2. Escrever `internal/subscriptions` seguindo T4.2–T4.5, usando `internal/digitalaccounts` (recém-
   -escrito) como template direto — mesma forma (domain/dto/mapper/service/handler/routes/postgres),
   adaptando os campos e adicionando o endpoint de soma mensal/anual.
3. Wire `internal/subscriptions` em `cmd/api/main.go` (composição `postgres.NewRepository` →
   `NewService` → `NewHandler` → `RegisterRoutes`, mesmo padrão usado para `digitalaccounts`).
4. Rodar `go build ./... && go vet ./... && gofmt -l .` de novo (comando funcionando, ver acima) —
   e, com um Postgres disponível, os testes de integração (`go test -tags=integration ./...`).
5. Só depois disso GAP 5.1 (extração genérica) e GAP 5.3 (job de alerta de vencimento) fazem sentido
   — ambos leem/escrevem nas tabelas que os passos 1–3 tornam utilizáveis de verdade.
6. Frontend (T1.5, T4.4, e o reset de navegação do `app-frontend-atlas` mencionado na conversa que
   gerou este documento) fica para depois do backend estar completo e testado — é trabalho
   independente que pode ser feito numa sessão dedicada, usando o canvas publicado
   (https://claude.ai/code/artifact/d13c014b-a7dc-4a7e-b1cc-cefc18182ee2) como referência visual.

---

## GAP 1 — Inventário de contas digitais

**O que falta:** não existe hoje nenhum módulo equivalente a "onde estão minhas contas" (GitHub,
bancos, streaming, etc.) com metadados de MFA/recuperação/status. `settings`/`integrations` são
outra coisa (integrações que o próprio Atlas consome — Google Drive, Telegram, provedores de IA —
não um catálogo das *suas* contas externas).

**Por que importa:** é o gap mais citado na visão original e o que mais reduz risco real (permite
responder "quais contas ainda usam meu Gmail antigo" e migrar progressivamente).

**Construir em cima de:** o padrão de módulo já validado em `categories`/`assets` (handler → service
→ `postgres.Repository`, DTO com tags `validate`, `FindByID` após `Create`/`Update` — D-014).
`people.PersonID` nullable já é o padrão para "pertence a alguém da família" (ver `assets.PersonID`,
`documents.PersonID`).

**Regra de design inegociável (veio da própria visão, e é a certa):** **nunca** armazenar senha.
Sem campo `password`/`secret` no schema — só metadados. Isso deve estar comentado no `domain.go`
como regra de design, não apenas seguido por convenção.

### Tarefas
- [ ] **T1.1 (P0, M)** — Nova migration `000008_digital_accounts.up/down.sql`: tabela
  `digital_accounts(id uuid pk, person_id uuid null fk→people, service_name varchar not null,
  category varchar CHECK, email_used varchar null, mfa_method varchar CHECK, recovery_notes text
  null, status varchar CHECK default 'ACTIVE', last_reviewed_at timestamptz null, created_at
  timestamptz default now())`. `category` CHECK sugerido:
  `DEVELOPMENT|BANKING|GOVERNMENT|STREAMING|SHOPPING|SOCIAL|GAMING|CLOUD|OTHER`. `mfa_method` CHECK:
  `NONE|PASSKEY|TOTP|SMS|APP|EMAIL`. `status` CHECK: `ACTIVE|INACTIVE|DELETED`. Índice em
  `person_id` (regra da seção 10.3 do `architecture-plan.md` — todo FK filha é indexada).
- [ ] **T1.2 (P0, M)** — Novo módulo `internal/digitalaccounts` (domain/dto/mapper/service/handler/
  routes/postgres), espelhando `internal/assets` como referência mais próxima em forma. Endpoints:
  `GET/POST /api/digital-accounts`, `PUT/DELETE /api/digital-accounts/{id}`.
- [ ] **T1.3 (P1, S)** — Filtro `GET /api/digital-accounts?email=...` — a pergunta "quais contas
  ainda usam meu Gmail antigo" que motivou o gap. `WHERE email_used = $1`, um índice extra em
  `email_used` se a tabela crescer.
- [ ] **T1.4 (P1, S)** — Ação `POST /api/digital-accounts/{id}/mark-reviewed` (seta
  `last_reviewed_at = now()`) — alimenta o contador "N não revisadas" do Mapa da Vida (GAP 6).
- [ ] **T1.5 (P1, M)** — Frontend: página `contas-digitais` (tabela com categoria/badge de MFA/
  status, filtro por e-mail, ação "marcar revisada"), item no `quick-create-menu` e no
  `command-palette`, espelhando a estrutura de `components/clientes/*`.
- [ ] **T1.6 (P2, S)** — Ferramenta no `assistant` (tool-calling): `contas_digitais_listar` (read) e
  `contas_digitais_criar` (write, com confirmação) — para cadastrar contas via chat durante a
  varredura inicial de e-mails antigos.

---

## GAP 2 — `documents` não tem os campos que a visão pede

**O que falta:** `Document` (`internal/documents/domain.go`) hoje só tem
`PersonID/OriginalFilename/FilePath/FileType/Origin/DocumentDate/Tags/AssetID/PetID/CreatedAt`. A
visão pede, por registro: nome, categoria, descrição, **data de emissão** *e* **data de validade**
(hoje só existe uma data), instituição, valor, observações, URL relacionada. Isso bloqueia
diretamente os gaps 3, 5 e 8 abaixo.

**Por que importa:** sem `expiration_date`/`institution`/`amount`, não dá pra gerar alerta de
vencimento (seguro, garantia, contrato) nem pra padronizar nome de arquivo automaticamente.

**Construir em cima de:** `documents` já é dono de `AssetID`/`PetID`/`PersonID` — a enriquecida é
aditiva, não muda relações existentes. Padrão de coluna nullable já estabelecido em todo o módulo.

### Tarefas
- [ ] **T2.1 (P0, S)** — Migration `000009_documents_metadata.up/down.sql`: adicionar em
  `documents`: `description text null`, `institution varchar null`, `amount numeric(12,2) null`,
  `expiration_date timestamptz null`, `external_url text null`, `notes text null`. Todas nullable
  (documentos existentes não têm esses dados) — sem `NOT NULL`, sem backfill necessário.
- [ ] **T2.2 (P0, S)** — `documents.Document`/`DocumentDTO`/`CreateDocumentRequest`: adicionar os 6
  campos novos como ponteiros, seguindo exatamente o padrão de `AcquisitionValue *string` em
  `assets.Asset`. Atualizar `mapper.go`/`validation.go` (mesma disciplina de `go-playground/
  validator` que o resto do módulo já usa).
- [ ] **T2.3 (P1, S)** — Frontend: os formulários/diálogos de documento (ligados a
  `saude/exames-upload-dialog`, upload genérico) ganham os campos novos — instituição e data de
  validade em destaque (são os dois que alimentam alerta/mapa da vida).

---

## GAP 3 — Seguros / Garantias / Fiscal não são categorias de primeira classe

**O que falta:** `documents.Origin` hoje tem `INVOICE|PAYSTUB|LAB_RESULT|PERSONAL|ASSET|PET|OTHER`
— nada equivalente a seguro, garantia ou imposto de renda. Hoje esses documentos cairiam em
`OTHER`, sem tratamento diferenciado.

**Por que importa:** é o gap de **menor custo/maior retorno** da lista inteira — `Origin` **não tem
CHECK no banco** (comentário explícito em `domain.go`: "the documents.origin column has no CHECK
constraint... Validated as a closed set here at the Go layer"). Adicionar valores é uma mudança
Go-only, zero migration.

**Construir em cima de:** `Origin` já resolve exatamente este problema para `INVOICE`/`PAYSTUB`/
`LAB_RESULT` — é o mesmo padrão, só mais 3 valores. `AssetID` já existe para ligar
`WARRANTY`→bem específico (a garantia de um bem já tem pra onde apontar).

### Tarefas
- [ ] **T3.1 (P0, XS)** — `documents/domain.go`: adicionar `OriginInsurance Origin = "INSURANCE"`,
  `OriginWarranty Origin = "WARRANTY"`, `OriginTax Origin = "TAX"` (Fiscal/IR). Atualizar a tag
  `oneof` do `validation.go`/`dto.go`.
- [ ] **T3.2 (P1, S)** — Depende de GAP 2: para `WARRANTY`, `expiration_date` é a data que fecha a
  garantia (já pode usar `AssetID` existente pra "garantia deste bem"); para `INSURANCE`,
  `institution` = seguradora, `amount` = prêmio, `expiration_date` = vencimento da apólice; para
  `TAX`, `expiration_date` = prazo de entrega/guarda.
- [ ] **T3.3 (P2, S)** — Frontend: badges/ícones por `Origin` já devem existir para os 7 valores
  atuais — só estender a lista, sem tela nova.

---

## GAP 4 — Central de assinaturas

**O que falta:** nenhum módulo equivalente. `expenses` registra o gasto quando ele acontece, mas não
modela "isso se repete todo mês, no dia X, por R$ Y, e cancela quando eu quiser" nem soma
mensal/anual dedicada.

**Por que importa:** é o item mais citado como "vai revelar gasto invisível" — a soma de assinaturas
esquecidas costuma surpreender.

**Construir em cima de:** `notifications` (GAP já tem `Repository.Create` genérico — só falta algo
que dispare a criação); `dashboard.Service.Resumo` já tem `ExpensesSummary`/`IncomeSummary` — um
`SubscriptionsSummary` é o mesmo padrão.

### Tarefas
- [ ] **T4.1 (P1, M)** — Migration `000010_subscriptions.up/down.sql`: tabela
  `subscriptions(id uuid pk, person_id uuid null fk→people, name varchar not null, category varchar
  CHECK, amount numeric(12,2) not null, billing_cycle varchar CHECK, next_renewal_date timestamptz
  not null, payment_method varchar null, status varchar CHECK default 'ACTIVE', notes text null,
  created_at timestamptz default now())`. `category` CHECK: `STREAMING|SOFTWARE|DOMAIN|CLOUD|
  GYM|OTHER`. `billing_cycle` CHECK: `WEEKLY|MONTHLY|YEARLY`. `status` CHECK:
  `ACTIVE|CANCELLED|PAUSED`. Índice em `person_id` e em `next_renewal_date` (a query "vencendo em N
  dias" varre por essa coluna).
- [ ] **T4.2 (P1, M)** — Novo módulo `internal/subscriptions`, mesma forma de `internal/assets`.
  Endpoint extra `GET /api/subscriptions/summary` → soma mensal (normalizando `YEARLY`/`WEEKLY` pra
  equivalente mensal) e soma anual — mesma ideia de `expenses.MonthlySummaryDTO`.
- [ ] **T4.3 (P1, S)** — Depende de um mecanismo de alerta (GAP 5.3 abaixo) para gerar notificação
  N dias antes de `next_renewal_date`.
- [ ] **T4.4 (P1, M)** — Frontend: página `assinaturas` com tabela + card de total mensal/anual
  (reaproveitando o padrão visual de `patrimonio-resumo-card`/`kpi-card`).
- [ ] **T4.5 (P2, S)** — `dashboard.ResumoDTO` ganha `SubscriptionsSummary` (soma mensal) pro
  resumo da home, mesmo padrão de `ExpensesSummary`.

---

## GAP 5 — Inbox com IA é real, mas cobre só 3 origens fixas

**O que já existe (não é gap, é base a estender):** `documents.Service.Extract`
(`internal/documents/extract.go`) já lê PDF, manda pro LLM ("Pandora") e cria automaticamente
despesa/receita/registro de saúde para `INVOICE`/`PAYSTUB`/`LAB_RESULT`, best-effort (nunca bloqueia
o upload). O frontend já tem os diálogos de confirmação (`informacoes-extraidas-dialog`,
`marcadores-extraidos-dialog`). A visão pedia exatamente essa UX — ela já existe para esses 3 casos.

**O que falta de fato:**
1. Qualquer outro `Origin` (`PERSONAL`/`ASSET`/`PET`/`OTHER`/os 3 novos do GAP 3) cai no `default:`
   de `Extract` — "no structured extraction target yet" — e não preenche nem os campos genéricos do
   GAP 2 (instituição/valor/data de validade).
2. PDF escaneado (sem camada de texto) é **explicitamente pulado hoje** — o próprio código documenta
   isso: `"Scanned/image PDF — no text layer... Needs OCR/vision (not available locally yet)"`. Sem
   OCR, boa parte de nota fiscal/comprovante escaneado nunca é processada.
3. Não existe alerta automático de vencimento — nada hoje lê `expiration_date` (quando existir, GAP
   2) e cria uma `notifications.Notification`.

### Tarefas
- [ ] **T5.1 (P0, M)** — Depende de GAP 2. Adicionar `extractGeneric` em `extract.go`: para
  qualquer `Origin` sem extração estruturada dedicada, pedir ao LLM só
  `{"institution":"...","amount":"...","expirationDate":"YYYY-MM-DD","description":"..."}` e
  gravar direto nas colunas novas do próprio `Document` (via `s.repo.Update`), sem criar registro em
  outro módulo. Isso faz TODO documento (não só os 3 originais) sair do upload já parcialmente
  preenchido — é o "Inbox" da visão, generalizado.
- [ ] **T5.2 (P1, L)** — OCR/vision para PDF escaneado: hoje `extractPDFText` retorna `""` e a
  extração é pulada. Precisa de um passo de OCR (Tesseract local, ou vision model via `pandora` se o
  provedor conectado suportar imagem) antes de cair no fallback. Maior esforço do documento inteiro —
  vale isolar como tarefa própria, não bloquear o resto do GAP 5 nela.
- [ ] **T5.3 (P0, M)** — Job de alerta de vencimento: novo `cmd/worker` job (reusa
  `internal/jobs` — picker + lease, já existe o padrão) que roda diariamente,
  `SELECT * FROM documents WHERE expiration_date BETWEEN now() AND now() + interval '30 days'`
  (e o mesmo para `subscriptions.next_renewal_date` do GAP 4), e chama
  `notifications.Service.Create` para cada um ainda não notificado (precisa de uma forma de não
  duplicar — ex. checar se já existe notificação com o mesmo `Link` apontando pro documento/
  assinatura antes de criar). Mesmo idioma de "nunca reprocessar o que já foi processado" que o
  resto do jobs system já segue.
- [ ] **T5.4 (P2, S)** — Ferramenta no `assistant`: `documentos_pendentes_revisao` (read) — lista
  documentos que a extração automática não conseguiu preencher, pra IA sugerir no chat.

---

## GAP 6 — "Mapa da Vida" (tela de completude por área)

**O que já existe (base forte):** `dashboard.Service.Resumo` já centraliza tudo que a home precisa
numa chamada só (`ResumoDTO`), e **já documenta os campos que faltam**: `Insights`/
`Recommendations` estão deliberadamente vazios hoje (`dashboard/dto.go`, comentário de `ResumoDTO`:
"a rule-based financial-analysis feature... not yet built"). O dashboard já mostra "próximos
vencimentos" e "saúde em dia" como cards — é literalmente o esqueleto do Mapa da Vida, só sem a
visão agregada "por área, o que está completo/pendente".

**O que falta:** um endpoint que responda, por área, uma contagem de pendências — não é dado novo,
é uma leitura agregada sobre dados que já existem (mais os novos dos gaps 1/4).

### Tarefas
- [ ] **T6.1 (P1, M)** — Novo endpoint `GET /api/dashboard/completeness` em `internal/dashboard`,
  retornando por área um status computado (sem armazenar nada novo — só `COUNT`/`WHERE` sobre
  tabelas existentes):
  - Identidade/Carreira/Educação: contagem de `person_information` por `Category` vs. um conjunto
    de chaves esperadas (`RG`, `CPF`, ...) — "completo" é heurístico, não uma trava rígida.
  - Patrimônio: `assets` sem nenhum `documents` vinculado (`AssetID`) — "bem sem nota fiscal".
  - Contas digitais (GAP 1): `COUNT(*) WHERE last_reviewed_at IS NULL OR last_reviewed_at < now() -
    interval '180 days'`.
  - Assinaturas (GAP 4): `COUNT(*) WHERE status = 'ACTIVE'` a revisar (sem critério de "revisada"
    ainda — reaproveitar `last_reviewed_at` do mesmo jeito se fizer sentido).
  - Documentos vencendo (GAP 2/5): `COUNT(*) WHERE expiration_date < now() + interval '30 days'`.
- [ ] **T6.2 (P1, M)** — Frontend: página/seção "Mapa da Vida" com uma linha por área (nome, status
  ✓/⚠, contagem, link pra tela filtrada) — mesmo padrão visual de `insight-card-grid`/`status-donut-
  chart` que já existem no dashboard atual.
- [ ] **T6.3 (P2, M)** — Preencher de fato `ResumoDTO.Insights`/`Recommendations` (hoje vazios por
  design) com regras simples derivadas do mesmo endpoint acima ("3 assinaturas não usadas há 60
  dias", "seguro do carro vence em 12 dias") — é a extensão natural do T6.1, não um recurso novo.

---

## GAP 7 — Kit de Emergência

**O que falta:** nenhuma tela/endpoint agrega "o que alguém precisaria saber rapidamente" — mas
todos os dados já existem em módulos separados (`health` tem tipo sanguíneo/condições/medicações,
`personinfo` tem RG/CPF, `documents` com `Origin=INSURANCE` (GAP 3) tem a apólice, `people` tem
contatos/relações, `pets` tem dados de vacina/veterinário).

**Por que importa:** baixo custo (é 100% agregação, zero dado novo) e alto valor simbólico — mas
depende dos gaps 2/3 para o seguro/plano de saúde aparecerem ali de forma estruturada.

### Tarefas
- [ ] **T7.1 (P2, M)** — Depende de GAP 3. Endpoint `GET /api/people/{id}/emergency-kit`
  (provavelmente vive em `dashboard`, mesmo padrão de `PersonDetailDTO` que já agrega
  health+documents+assets+personinfo de várias fontes numa chamada só) retornando: tipo
  sanguíneo/alergias/condições (de `health`), RG/CPF (de `personinfo`), documentos com
  `Origin=INSURANCE` da pessoa, pets da família com dados de vacina, contatos (de `people`).
  Somente leitura — nenhuma tabela nova.
- [ ] **T7.2 (P2, S)** — Frontend: página `/emergencia/{pessoaId}` pensada pra ser lida rápido por
  outra pessoa (tipografia grande, sem navegação de app, opcionalmente exportável/imprimível).

---

## GAP 8 — Padronização automática de nome de arquivo no upload

**O que falta:** `documents` armazena `OriginalFilename`/`FilePath` como recebidos — nada gera o
`AAAA-MM-DD - Categoria - Instituição - Descrição.pdf` que a visão pede.

**Depende de:** GAP 2 (`institution`/`description` precisam existir antes de compor o nome) e
idealmente GAP 5.1 (a extração genérica é o que preenche esses campos automaticamente pra maioria
dos uploads — sem ela, o usuário teria que digitar instituição/descrição toda vez).

### Tarefas
- [ ] **T8.1 (P2, S)** — Em `documents/storage.go` (ou no ponto do `handler.go` que grava o
  arquivo), gerar o nome canônico a partir de `DocumentDate` (ou `now()` se ausente) +
  `Origin` (traduzido pra rótulo em português: `INVOICE`→`Fatura`, `INSURANCE`→`Seguro`, etc.) +
  `Institution` + `Description`, sane­tizando caracteres inválidos de filesystem. Guardar como
  `FilePath`; manter `OriginalFilename` como já é hoje (nome enviado pelo usuário, auditável).
- [ ] **T8.2 (P2, S)** — Aplicar retroativamente é opcional e arriscado (renomear arquivo já
  referenciado por `FilePath` em produção) — se o usuário quiser, isso é uma tarefa de migração de
  dados separada, não parte do upload flow. Não fazer por padrão.

---

## Resumo — sequenciamento sugerido

A ordem abaixo respeita dependências reais (não é só prioridade solta):

1. **GAP 2** (T2.1–T2.3) — colunas novas em `documents`. Tudo mais depende disso.
2. **GAP 3** (T3.1) — 3 valores novos de `Origin`. Trivial, mesma migration/PR do passo 1 se quiser.
3. **GAP 1** (T1.1–T1.5) — módulo de contas digitais, independente dos outros, pode andar em
   paralelo aos passos 1–2.
4. **GAP 5.1 e 5.3** — extração genérica + job de alerta de vencimento, agora que `documents` tem
   onde gravar e o que checar.
5. **GAP 4** (T4.1–T4.4) — assinaturas, mesma forma dos módulos existentes, reaproveita o job de
   alerta do passo 4 pra `next_renewal_date`.
6. **GAP 6** (T6.1–T6.2) — Mapa da Vida, agora que contas digitais/assinaturas/documentos com
   vencimento existem pra agregar.
7. **GAP 7** (T7.1–T7.2) — Kit de Emergência, mesma lógica de agregação do passo 6.
8. **GAP 5.2** (OCR) e **GAP 8** (nome de arquivo automático) — maior esforço / menor urgência,
   ficam por último ou paralelos a qualquer momento livre.

Nenhuma dessas tarefas exige tocar em `people`/`personrelations`/`categories`/`platform`/
`migrations` além de somar migrations novas (`000008`+) — segue exatamente o mesmo padrão que todo
módulo existente já usa, sem redesenhar nada da arquitetura atual.
