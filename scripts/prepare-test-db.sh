#!/usr/bin/env bash
# Prepara um banco para os testes de integração (os *_integration_test.go e afins).
#
# Esses testes leem TEST_DATABASE_URL e esperam as tabelas de domínio já criadas. Elas vivem no
# schema de um tenant, e o jeito mais fiel de criar esse schema é o mesmo da produção: subir a API
# (que roda as migrations de controle) e fazer um cadastro (que cria o schema e roda as migrations
# do tenant). Ao final, imprime a TEST_DATABASE_URL pronta para uso.
#
# Uso: scripts/prepare-test-db.sh "postgres://postgres@localhost:5432/postgres?sslmode=disable" [nome_do_banco]
set -euo pipefail

ADMIN_URL="${1:?informe a URL de administrador do Postgres}"
DB_NAME="${2:-casa_test}"
SLUG="testes"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'kill "${API_PID:-0}" 2>/dev/null || true; rm -rf "$WORK"' EXIT

# Troca o nome do banco na URL de administrador (preserva usuário, host, porta e parâmetros).
DB_URL="$(python3 -c 'import sys,urllib.parse as u; p=u.urlparse(sys.argv[1]); print(u.urlunparse(p._replace(path="/"+sys.argv[2])))' "$ADMIN_URL" "$DB_NAME")"

psql "$ADMIN_URL" -qc "DROP DATABASE IF EXISTS $DB_NAME WITH (FORCE)" >/dev/null
psql "$ADMIN_URL" -qc "CREATE DATABASE $DB_NAME" >/dev/null

(cd "$ROOT" && go build -o "$WORK/api" ./cmd/api)
PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')"
DATABASE_URL="$DB_URL" PORT="$PORT" APP_FRONTEND_ORIGIN="http://127.0.0.1:$PORT" \
  APP_ENCRYPTION_KEY="$(head -c32 /dev/urandom | base64)" TENANT_MASTER_KEY="$(head -c32 /dev/urandom | base64)" \
  APP_UPLOADS_DIR="$WORK/uploads" APP_BACKUPS_DIR="$WORK/backups" SIGNUP_ENABLED=true \
  "$WORK/api" >"$WORK/api.log" 2>&1 &
API_PID=$!

for _ in $(seq 1 60); do
  curl -s -o /dev/null "http://127.0.0.1:$PORT/api/auth/me" && break
  sleep 0.5
done

STATUS="$(curl -s -o "$WORK/signup.json" -w '%{http_code}' -H 'Content-Type: application/json' \
  -d "{\"slug\":\"$SLUG\",\"email\":\"testes@example.com\",\"senha\":\"senha-segura-123\"}" \
  "http://127.0.0.1:$PORT/api/auth/signup")"
if [ "$STATUS" != "201" ]; then
  echo "cadastro falhou (HTTP $STATUS):" >&2
  cat "$WORK/signup.json" "$WORK/api.log" >&2
  exit 1
fi

SEP='?'; case "$DB_URL" in *\?*) SEP='&';; esac
echo "${DB_URL}${SEP}options=-csearch_path%3Dtenant_${SLUG},public"
