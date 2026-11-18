#!/usr/bin/env bash
# One-shot ETL: builds testdata/seed.sql — the new single-tenant schema (000001) populated with the
# user's real production data, translated per docs/schema-migration-map.md.
#
# Steps:
#   1. Start a throwaway Postgres.
#   2. Restore prod-dump.sql into schema "legacy" (production shape: pt, multi-tenant).
#   3. Apply every internal/migrations/*.up.sql in order into schema "public" (new shape: en,
#      single-tenant) — NOT just 000001; forgetting a later migration here silently produces a
#      seed.sql missing columns/tables the current code already expects.
#   4. Run the transform SQL (transform.sql) that INSERTs legacy -> public with translations.
#   5. pg_dump public -> testdata/seed.sql (the fixture future tests restore).
#
# No production data is modified; the homelab is never touched by this script.
set -euo pipefail

# Git Bash on Windows rewrites absolute-looking paths (e.g. "/tmp/custom.dump") in command
# arguments into Windows paths before they ever reach `docker exec` — breaks any path meant for
# the CONTAINER's filesystem. Harmless/unset on real Unix shells.
export MSYS_NO_PATHCONV=1

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DUMP="$REPO_ROOT/testdata/prod-dump.sql"
SEED="$REPO_ROOT/testdata/seed.sql"
MIGRATIONS_DIR="$REPO_ROOT/internal/migrations"
TRANSFORM="$REPO_ROOT/scripts/etl/transform.sql"

PGPASSWORD=test
PORT="${PORT:-55440}"
CID=""

cleanup() { [ -n "$CID" ] && docker rm -f "$CID" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== starting postgres =="
CID=$(docker run -d -e POSTGRES_PASSWORD=test -e POSTGRES_DB=etl -p "$PORT:5432" postgres:16)
sleep 6

echo "== preparing schemas =="
docker exec -i "$CID" psql -U postgres -d etl -c "CREATE SCHEMA legacy;"

# testdata/prod-dump.sql is normally a plain-SQL dump (pg_dump default format), which the sed
# rewrite below operates on directly — but a fresh dump handed over as pg_dump -Fc ("custom
# format", e.g. a .dump file from `pg_dump -Fc` or a GUI backup tool) isn't text at all. Detect and
# convert transparently rather than failing confusingly on the sed step.
if file "$DUMP" 2>/dev/null | grep -q "PostgreSQL custom database dump"; then
  echo "== \$DUMP is custom-format (pg_dump -Fc) — converting to plain SQL first =="
  CONVERTED="$(mktemp -u).sql"
  docker exec -i "$CID" sh -c "cat > /tmp/custom.dump" < "$DUMP"
  docker exec -i "$CID" pg_restore -f /tmp/plain.sql --no-owner --no-privileges /tmp/custom.dump
  docker exec -i "$CID" cat /tmp/plain.sql > "$CONVERTED"
  DUMP="$CONVERTED"
fi

# Restore the production dump into schema legacy (rewrite public. -> legacy.).
echo "== restoring prod-dump.sql into legacy =="
sed -E 's/\bpublic\./legacy./g; s/COPY public\./COPY legacy./g' "$DUMP" \
  | docker exec -i "$CID" psql -U postgres -d etl -v ON_ERROR_STOP=1 >/dev/null

echo "== applying new schema (every migration, in order) into public =="
for mig in "$MIGRATIONS_DIR"/*.up.sql; do
  echo "   -- $(basename "$mig")"
  docker exec -i "$CID" psql -U postgres -d etl -v ON_ERROR_STOP=1 < "$mig" >/dev/null
done

echo "== running transform legacy -> public =="
docker exec -i "$CID" psql -U postgres -d etl -v ON_ERROR_STOP=1 < "$TRANSFORM" >/dev/null

# Seed a minimal member_permissions row for the demo MEMBER (sanches57@uni9.edu.br) so the
# RequirePerm "MEMBER allowed" path is exercisable end-to-end (D-012, option 1). The Java prod DB
# had no member_permissions table (perms lived in tenant_members), so the ETL emits none — without
# this, every MEMBER is denied everywhere. Grants read+write on 'expenses' only.
docker exec -i "$CID" psql -U postgres -d etl -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
INSERT INTO member_permissions (user_id, module, can_read, can_write, created_at)
SELECT id, 'expenses', true, true, now()
FROM app_users
WHERE role = 'MEMBER'
ON CONFLICT DO NOTHING;
SQL

echo "== dumping public -> seed.sql (data only; ordering follows FK dependencies) =="
docker exec -i "$CID" pg_dump -U postgres -d etl --schema=public --data-only --no-owner --no-privileges \
  > "$SEED"

echo "== seed.sql written: $(wc -l < "$SEED") lines =="
echo "== done =="
