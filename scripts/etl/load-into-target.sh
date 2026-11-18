#!/usr/bin/env bash
# Formalizes the real cutover ETL's LOAD half — the exact recipe already validated manually
# against atlas-test-db, atlas-prod-mirror, and the homelab's atlas-postgres earlier in this
# migration (see docs/homelab-deploy.md): apply every migration, then load testdata/seed.sql with
# FK checks off (categories has a circular self-FK — see build-seed.sh's own warning about this).
#
# Meant to run wherever it can reach the target Postgres via `docker exec` — for the real
# production cutover that's ON the homelab host itself (Postgres there isn't network-reachable,
# only local docker exec — see docs/homelab-deploy.md), so this script is designed to be copied
# there (scp) alongside a freshly-generated testdata/seed.sql (see build-seed.sh) and run via SSH,
# not run against a remote DATABASE_URL over the network.
#
# Usage: ./load-into-target.sh [container] [db_user] [db_name]
#   container  Postgres container name (default: atlas-go-atlas-postgres-1, the homelab's)
#   db_user    (default: atlas)
#   db_name    (default: atlas_prod)
set -euo pipefail

CONTAINER="${1:-atlas-go-atlas-postgres-1}"
DB_USER="${2:-atlas}"
DB_NAME="${3:-atlas_prod}"

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MIGRATIONS_DIR="$REPO_ROOT/internal/migrations"
SEED="$REPO_ROOT/testdata/seed.sql"

if [ ! -f "$SEED" ]; then
  echo "[ERRO] $SEED não existe — rode scripts/etl/build-seed.sh primeiro (a partir de um dump" >&2
  echo "       fresco de produção) pra gerar o fixture antes de carregar." >&2
  exit 1
fi

if ! docker inspect "$CONTAINER" >/dev/null 2>&1; then
  echo "[ERRO] container '$CONTAINER' não encontrado — confirme o nome (docker ps)." >&2
  exit 1
fi

echo "== aplicando migrations em $CONTAINER/$DB_NAME =="
for mig in "$MIGRATIONS_DIR"/*.up.sql; do
  echo "   -- $(basename "$mig")"
  docker exec -i "$CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 < "$mig" > /dev/null
done

echo "== carregando seed.sql (FK checks desligado por causa do self-FK circular de categories) =="
docker exec -i "$CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -c "SET session_replication_role = replica;"
docker exec -i "$CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 < "$SEED"

echo "== validação: contagem de linhas nas tabelas principais =="
docker exec "$CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -t -c "
  SELECT 'clients', count(*) FROM clients
  UNION ALL SELECT 'loans', count(*) FROM loans
  UNION ALL SELECT 'installments', count(*) FROM installments
  UNION ALL SELECT 'loan_transactions', count(*) FROM loan_transactions
  UNION ALL SELECT 'people', count(*) FROM people
  UNION ALL SELECT 'documents', count(*) FROM documents;
"

echo "== done =="
