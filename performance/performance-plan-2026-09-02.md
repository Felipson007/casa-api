# Plano de melhoria de performance — 2026-09-02

## Baseline medido

Execução local do binário Atlas contra um snapshot autorizado do PostgreSQL do homelab,
restaurado em banco isolado. O snapshot tinha 39 tabelas, 2 usuários, 26 empréstimos,
1.054 despesas e 127 receitas. O teste foi feito pelo container oficial `grafana/k6`
v2.2.0, com 1–20 VUs, ramp-up, 30 s steady state e spike.

| Métrica | Resultado |
|---|---:|
| Requisições | 18.348 |
| Vazão | 228,5 req/s |
| Erros HTTP | 0% |
| Latência global p95 / p99 | 37,3 ms / 44,3 ms |
| Dashboard p95 / p99 | 44,5 ms / 52,3 ms |
| Health p95 / p99 | 3,35 ms / 11,79 ms |
| Maior latência observada | 71,5 ms |

O primeiro resultado autenticado teve 36,36% de falha porque quatro endpoints foram
chamados sem os parâmetros obrigatórios `year/month` ou `ano/mes`; isso foi corrigido
no script e não representa falha do serviço. Uma execução anterior enviou cookie em
vez de Bearer e produziu 401; o teste agora usa o caminho documentado para scripts.

## Evidências de profiling

O snapshot heap em repouso após a carga reportou 10,6 MB em uso. O top foi:

| Item | Participação | Leitura |
|---|---:|---|
| `runtime.mallocgc` | 43,68% | sinal de alocação agregada; precisa de perfil CPU/allocs com amostragem antes de atribuir a um endpoint |
| `context.withCancel` | 9,68% | provável churn de contextos em chamadas concorrentes; investigar somente se crescer com VUs |
| `bytes.growSlice` | 7,65% | respostas/serialização ou buffers; comparar endpoints e tamanho de payload |
| `regexp.compile` | 9,79% cumulativo | verificar regex compilada por chamada e mover padrões invariáveis para variáveis globais |

Durante uma captura pprof, o health p99 chegou a 102,9 ms em uma rodada; sem essa
interferência voltou a 11,79 ms. Profiling deve ser tratado como modo de diagnóstico
com overhead, nunca como medição de aceite.

## Plano priorizado

### P0 — corrigir a medição e observabilidade (1 dia)

1. Manter os parâmetros obrigatórios e o Bearer no k6; adicionar checks explícitos para
   status 400/401 e um cenário de contrato que falhe com mensagem clara.
2. Emitir métricas de duração por handler e por query (sem payloads ou tokens), além de
   `db pool acquire wait`, conexões ocupadas e fila de jobs.
3. Repetir baseline em três execuções e registrar mediana/p95/p99, CPU, RSS, GC e
   goroutines. O baseline atual é válido para a máquina local e o dataset copiado, não
   é ainda um SLO de produção.

### P1 — dashboard e banco (2–4 dias)

1. Instrumentar cada chamada do `internal/dashboard/Service`; o dashboard é o caminho
   mais lento medido (p95 44,5 ms) e compõe várias áreas.
2. Capturar `EXPLAIN (ANALYZE, BUFFERS)` das queries do dashboard, `loans/vigentes`,
   resumos mensais e listas com o mesmo volume do snapshot.
3. Adicionar paginação/limite explícito nas listas que hoje materializam slices inteiros;
   validar índices pelas colunas de filtro, data e ordenação antes de criar índices.
4. Avaliar paralelização limitada das chamadas independentes do dashboard, com
   `errgroup`, contexto cancelável e limite de concorrência; medir pool wait para não
   trocar latência por saturação do PostgreSQL.

### P1 — alocações e payloads (2–3 dias)

1. Gerar perfis `allocs`, `inuse_space` e CPU por endpoint, comparando `dashboard`,
   `expenses`, `income` e `transactions`.
2. Investigar `bytes.growSlice`, `encoding/json` e `regexp.compile`; compilar regex
   invariáveis uma vez e evitar cópia/serialização intermediária onde o perfil provar
   impacto.
3. Medir tamanho das respostas e introduzir paginação/campos resumidos, sobretudo para
   1.054 despesas e históricos de transações.

### P2 — resiliência de dependências (3–5 dias)

1. Executar Toxiproxy com 1,5 s de latência e timeout de 1 s no banco; validar que os
   requests terminam, que não há crescimento monotônico de goroutines e que o health
   retorna em até 30 s após recuperação.
2. Definir timeouts explícitos para OmniRoute, Google Drive, Immich e SteamPlaybook;
   separar retries com backoff de operações idempotentes.
3. Verificar fila/concorrência do `jobs.Runner` sob geração de PDF/Excel e extração de
   documentos; impedir que goroutines desacopladas acumulem sem backpressure.

### P2 — carga sustentada e limites operacionais (1–2 dias)

1. Executar o perfil completo padrão de ~14m15s em homologação representativa, com
   `STEADY_VUS` progressivo até encontrar o ponto de saturação.
2. Repetir spike com CPU/memória limitados conforme o override de caos e registrar
   crash-loop, OOM, pool exhaustion e tempo de recuperação.
3. Ajustar thresholds somente após observar o p95/p99 de produção; preservar como
   meta inicial `<1%` de erro, global p95 `<300 ms`, p99 `<750 ms` e dashboard p99
   `<1,2 s`.

## Critério de conclusão

O plano estará concluído quando três execuções reproduzíveis passarem os thresholds,
os perfis não mostrarem crescimento de heap/goroutines após a carga, as queries críticas
tiverem plano indexado validado e os cenários de latência/timeout recuperarem sem
intervenção manual. Qualquer otimização deve vir acompanhada de comparação antes/depois.

## Execução deste plano

Implementado nesta branch:

- `dashboard.Resumo` executa dependências independentes com `errgroup`, cancelamento
  propagado e limite de quatro operações concorrentes.
- Cada dependência do dashboard registra duração/erro com `request_id`; aquisições lentas
  do pool PostgreSQL registram duração e estado do pool.
- Regex de parsing monetário foi movida para compilação única, fora do caminho quente.
- O k6 ganhou modo `QUICK=1` e os parâmetros obrigatórios dos endpoints foram corrigidos.

Comparação no mesmo snapshot e perfil de carga:

| Métrica | Antes | Depois | Variação |
|---|---:|---:|---:|
| Requisições | 18.403 | 18.876 | carga equivalente |
| Vazão | 229,3 req/s | 235,9 req/s | +2,9% |
| Global p95 | 37,0 ms | 32,2 ms | -13,0% |
| Global p99 | 45,3 ms | 37,9 ms | -16,3% |
| Dashboard p95 | 45,6 ms | 30,9 ms | -32,2% |
| Dashboard p99 | 51,4 ms | 38,2 ms | -25,7% |
| Erros HTTP | 0% | 0% | mantido |

Os planos das listas no snapshot foram baratos (`expenses` 0,50 ms, `income` 0,03 ms,
`loans` 0,02 ms) e usaram índices/seq scan adequados ao volume atual; nenhum índice
especulativo foi criado. Paginação continua recomendada para crescimento do dataset,
especialmente `expenses` (1.054 linhas), mas requer evolução explícita do contrato de API.

### Resultados da execução completa e caos local

No snapshot local restaurado a partir de produção, o perfil completo de 14m15s gerou
481.250 requisições, 566,1 RPS médios e 0% de falhas HTTP. O p95 global foi 46,8 ms;
o p95 do dashboard foi 58,8 ms. Durante o spike, `/health` atingiu p95 de 157,4 ms e
máximo de 966,9 ms, violando o threshold de p99 < 100 ms e fazendo o k6 encerrar com
falha de threshold. Isso caracteriza degradação de cauda sob pico, não falha funcional.

No Toxiproxy local, o dashboard respondeu em 175 ms no baseline e em 168 ms após
recuperação. Com 1,5 s de latência no PostgreSQL, excedeu o timeout de 8 s do cliente;
com timeout de banco de 1 s, falhou em aproximadamente 2,05 s. Após remover os toxics,
voltou a HTTP 200 sem reinício manual.

Próximas ações priorizadas: configurar timeout por operação e circuit breaker para
dependências; investigar a cauda do `/health` durante o spike com CPU, goroutines e
pool em três repetições; definir SLOs separados para health e rotas dependentes de
banco; e repetir o perfil completo com limites de CPU/memória. Paginação continua
recomendada quando o volume crescer; os planos atuais não justificam novos índices.
