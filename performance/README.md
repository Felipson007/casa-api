# Suíte de performance, resiliência e profiling

Esta suíte cobre o caminho HTTP real do Atlas, usando k6 para carga e Toxiproxy para
degradação controlada do PostgreSQL. Os testes exigem uma instância de homologação
com dados representativos. Não execute carga ou caos contra produção.

## Estrutura

```text
performance/
├── README.md
├── k6/atlas.js                 # ramp-up, carga constante e spike
├── k6/smoke.js                 # verificação rápida do liveness
└── chaos/
    ├── docker-compose.chaos.yml # override isolado com Toxiproxy
    ├── chaos.ps1                # latência, timeout, CPU/memória e recuperação
    └── README.md
```

## Carga E2E

O script exercita `/health`, dashboard (`kpis` e `resumo`), empréstimos, transações,
despesas, receitas e notificações. Os fluxos CRUD/escrita e geração de relatórios
devem ser executados em uma janela separada, com IDs e payloads de seed conhecidos,
porque são mutáveis e podem gerar jobs/PDFs.

```powershell
# Smoke sem autenticação: confirma que a API está viva.
k6 run -e BASE_URL=http://localhost:8081 performance/k6/smoke.js

# Sessão real do ambiente de teste. AUTH_COOKIE deve conter atlas_session=...
k6 run `
  -e BASE_URL=http://localhost:8081 `
  -e AUTH_COOKIE="atlas_session=$env:ATLAS_SESSION" `
  -e STEADY_VUS=20 -e STEADY_DURATION=5m -e SPIKE_VUS=100 `
  --summary-export=artifacts/k6-summary.json `
  performance/k6/atlas.js
```

O cenário completo dura aproximadamente 14m15s. Os thresholds iniciais são: falha
total abaixo de 1%, p95 global abaixo de 300 ms, p99 global abaixo de 750 ms,
dashboard p95 abaixo de 500 ms/p99 abaixo de 1,2 s e health p99 abaixo de 100 ms.
Eles são hipóteses de aceite e devem ser recalibrados com hardware e dataset de produção.

Para medir uma rota escrita ou relatório, use um script k6 específico com fixture,
idempotência e cleanup. Nunca coloque senha, cookie ou token no arquivo; passe-os por
variável de ambiente e não publique o resumo que contenha headers.

## Profiling Go com `net/http/pprof`

O listener de profiling é opt-in e separado do listener público. Inicie a API com
`PPROF_ADDR=127.0.0.1:6060`; em Docker, publique essa porta apenas em uma rede de
diagnóstico. O código não adiciona pprof ao mux da API.

```powershell
$env:PPROF_ADDR = '127.0.0.1:6060'
go run ./cmd/api

# Durante uma carga reproduzível:
go tool pprof -seconds=30 http://127.0.0.1:6060/debug/pprof/profile
go tool pprof http://127.0.0.1:6060/debug/pprof/heap
go tool pprof http://127.0.0.1:6060/debug/pprof/allocs
go tool pprof http://127.0.0.1:6060/debug/pprof/block
go tool pprof http://127.0.0.1:6060/debug/pprof/mutex
go tool pprof http://127.0.0.1:6060/debug/pprof/goroutine
go tool pprof -http=127.0.0.1:9090 cpu.pprof
```

Para block/mutex, habilite amostragem no processo de diagnóstico antes da carga
(isso altera overhead): `runtime.SetBlockProfileRate(1)` e
`runtime.SetMutexProfileFraction(1)`. Para scheduler/GC:

```powershell
Invoke-WebRequest http://127.0.0.1:6060/debug/pprof/trace?seconds=5 -OutFile trace.out
go tool trace trace.out
```

Leia CPU para funções quentes, heap/allocs para retenção e churn, block/mutex para
esperas, goroutine para vazamentos e trace para GC/scheduler. Compare baseline e
mudança com o mesmo seed.

## Análise estática e gargalos prováveis

| Área observada | Complexidade/risco | Evidência no código | Ação objetiva |
|---|---|---|---|
| Dashboard agregado | `O(Σ consultas + Σ itens)` por requisição; fan-out de várias áreas | `internal/dashboard/service.go` compõe loans, expenses, income, people, documents, assets, finance e curator | medir cada chamada; paralelizar somente chamadas independentes com contexto cancelável; cache curto por usuário/período |
| Curator/digest | `O(n)` em agrupamentos, memória `O(n)` | `internal/curator/digest.go` cria mapas e slices para empréstimos, despesas, categorias e metas | limitar período/página no SQL e evitar histórico integral |
| Relações de pessoas | BFS esperado `O(V+E)`, memória `O(V+E)` | `internal/personrelations/derive.go` | preservar `visited`, impor limite de nós/profundidade e testar grafos cíclicos |
| Agendamento de empréstimos | `O(k)` por quantidade de parcelas | `internal/loans/schedule.go` | benchmark para `k` 12/60/360 e evitar recalcular séries persistidas |
| Extração de documentos | I/O externo longo; goroutine desacoplada pode acumular | `internal/documents/reconcile.go` usa `go s.Extract(context.WithoutCancel(...))` | observar goroutines e fila; impor concorrência/backpressure; preferir jobs duráveis |
| Relatórios PDF/Excel | CPU/alocações proporcionais às linhas | `internal/reports/*.go` e `internal/jobs/runner.go` | manter assíncrono, limitar payload, medir heap/CPU e fila cheia |
| PostgreSQL | custo depende de cardinalidade/índices; listas retornam slices completos | `internal/*/postgres/repository.go` | `EXPLAIN (ANALYZE, BUFFERS)`, paginação/limites e pool sob saturação |

Não foi encontrado loop quadrático explícito no caminho principal. O risco relevante é
custo agregado e fan-out; a confirmação deve vir de pprof, `pg_stat_statements` e planos reais.

## Critérios de aceite

| Critério | Alvo inicial | Como verificar |
|---|---:|---|
| Erros HTTP no steady state | `< 1%` | threshold `http_req_failed` do k6 |
| Latência global p95 / p99 | `< 300 ms` / `< 750 ms` | resumo k6 por cenário |
| Dashboard p95 / p99 | `< 500 ms` / `< 1,2 s` | tag `endpoint:dashboard` |
| Liveness p99 | `< 100 ms` e recupera | smoke durante e após caos |
| Timeout de dependência | erro limitado, sem requisição pendurada | Toxiproxy + logs + duração máxima do k6 |
| Recuperação | 200 no `/health` até 30 s após remover toxic | polling pós-`recover` |
| Goroutines | sem crescimento monotônico após 3 ciclos | `/debug/pprof/goroutine` antes/depois |
| Memória | sem crescimento não recuperável | heap pprof + `docker stats` |
| Spike | sem crash-loop e retorno ao baseline | `docker inspect`, logs e smoke pós-spike |

Os limites são uma baseline operacional, não garantia universal de latência; aprove-os
contra o hardware real, tamanho do banco e SLO do usuário.
