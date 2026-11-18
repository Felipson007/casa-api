# Resiliência e caos

O override liga o `atlas-api` ao PostgreSQL por um proxy Toxiproxy, sem alterar o
`docker-compose.homelab.yml` original. O teste é destrutivo apenas para a stack de
performance e não deve apontar para dados de produção.

```powershell
$env:DB_PASSWORD = 'senha-do-banco-de-teste'
./performance/chaos/chaos.ps1 start
./performance/chaos/chaos.ps1 slow-db
k6 run -e BASE_URL=http://localhost:8081 performance/k6/smoke.js
./performance/chaos/chaos.ps1 recover
./performance/chaos/chaos.ps1 db-timeout
k6 run -e BASE_URL=http://localhost:8081 performance/k6/smoke.js
./performance/chaos/chaos.ps1 recover
./performance/chaos/chaos.ps1 cpu-memory
k6 run -e BASE_URL=http://localhost:8081 performance/k6/atlas.js
./performance/chaos/chaos.ps1 recover
```

Critérios: requests devem terminar dentro do timeout do cliente, erros 5xx não devem
causar cascata, e o healthcheck deve voltar a 200 após `recover`. Colete `docker stats`,
`docker inspect` e logs durante cada janela. `stop` encerra somente o projeto Compose
selecionado.
