#!/usr/bin/env bash
#
# atlas-build.sh — roda tudo do homelab: VALIDA (testes), builda a imagem combinada (backend Go +
# SPA do frontend) e publica no GHCR. Nao depende do GitHub Actions em nada (nem runner, nem
# minutos, nem o gate de CI) — foi o pedido original: tirar o pipeline dos Actions.
#
# Fluxo:
#   1. fetch do HEAD de cada repo (backend + frontend);
#   2. se o par de HEADs nao mudou desde o ultimo build, sai barato;
#   3. roda os mesmos testes que o ci.yml rodava, mas aqui em Docker:
#        backend : gofmt -l, go vet, go test (unit) e go test -tags=integration (contra um
#                  Postgres descartavel, com as migrations aplicadas — igual ao service do CI);
#        frontend: npm ci, npm run lint, npm run typecheck, npm test;
#   4. so publica se TUDO passar. Falhou -> nao publica (head antigo continua no ar) e so tenta de
#      novo quando o HEAD mudar (marcador last-failed evita reprocessar o mesmo commit falho a
#      cada 2 min);
#   5. publica :latest + :<sha-backend> no GHCR. O CronJob atlas-auto-update (k3s) faz o deploy.
#
# Config fora do git, em /etc/atlas-build.env (0600):
#   GH_TOKEN=<PAT classic com repo + write:packages>   # so para git clone + docker login
#   ATLAS_BUILD_DIR=/var/lib/atlas-build               # opcional
#   GHCR_USER=gustavodvp                               # opcional
# Dependencias: docker, git, flock.
#
set -euo pipefail

ENV_FILE="${ATLAS_BUILD_ENV:-/etc/atlas-build.env}"
if [ ! -r "$ENV_FILE" ]; then
  echo "[atlas-build] $ENV_FILE nao encontrado/legivel — nada a fazer" >&2
  exit 0
fi
# shellcheck disable=SC1090
set -a; . "$ENV_FILE"; set +a

: "${GH_TOKEN:?defina GH_TOKEN em $ENV_FILE}"
BUILD_DIR="${ATLAS_BUILD_DIR:-/var/lib/atlas-build}"
GHCR_USER="${GHCR_USER:-gustavodvp}"

BACKEND_REPO="gustavodvp/app-golang-atlas"
FRONTEND_REPO="gustavodvp/app-frontend-atlas"
IMAGE="ghcr.io/gustavodvp/app-golang-atlas"
BRANCH="main"

SRC="$BUILD_DIR/src"
STATE="$BUILD_DIR/last-built"
FAILED="$BUILD_DIR/last-failed"
NET="atlas-build-net"
PG="atlas-build-pg"

log() { printf '[atlas-build] %s\n' "$*"; }

mkdir -p "$SRC"
chmod 700 "$BUILD_DIR" "$SRC" 2>/dev/null || true

# Lock: se o ciclo anterior ainda estiver rodando, nao empilha outro.
exec 9>"$BUILD_DIR/.lock"
if ! flock -n 9; then
  log "ciclo anterior ainda rodando — saindo"
  exit 0
fi

auth_url() { printf 'https://x-access-token:%s@github.com/%s.git' "$GH_TOKEN" "$1"; }

sync_repo() { # $1=owner/repo  $2=dir de checkout
  local repo="$1" dir="$2"
  if [ -d "$dir/.git" ]; then
    git -C "$dir" remote set-url origin "$(auth_url "$repo")"
    git -C "$dir" fetch --quiet --prune origin "$BRANCH"
  else
    git clone --quiet --no-checkout "$(auth_url "$repo")" "$dir"
    git -C "$dir" fetch --quiet origin "$BRANCH"
  fi
}

# --------------------------------------------------------------------------------------------------
# Validacao (o que o ci.yml fazia, rodando aqui em Docker)
# --------------------------------------------------------------------------------------------------

validate_backend() {
  local go="$SRC/atlas-go" rc=0
  log "validando backend: gofmt/vet/test unit"
  docker run --rm -v "$go:/app" -w /app -v atlas-build-gocache:/go/pkg/mod \
    golang:1.25-alpine sh -c 'test -z "$(gofmt -l .)" && go vet ./... && go test ./...' || rc=1
  [ "$rc" -eq 0 ] || return 1

  log "validando backend: integration (Postgres descartavel)"
  docker rm -f "$PG" >/dev/null 2>&1 || true
  docker run -d --name "$PG" --network "$NET" \
    -e POSTGRES_DB=atlas_test -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
    postgres:16-alpine >/dev/null
  local i
  for i in $(seq 1 40); do docker exec "$PG" pg_isready -U postgres -d atlas_test >/dev/null 2>&1 && break; sleep 1; done
  local mig
  for mig in "$go"/internal/migrations/*.up.sql; do
    docker exec -i "$PG" psql -U postgres -d atlas_test -v ON_ERROR_STOP=1 <"$mig" >/dev/null || rc=1
  done
  [ "$rc" -eq 0 ] || { docker rm -f "$PG" >/dev/null 2>&1 || true; return 1; }
  docker run --rm --network "$NET" -v "$go:/app" -w /app -v atlas-build-gocache:/go/pkg/mod \
    -e TEST_DATABASE_URL="postgres://postgres:postgres@$PG:5432/atlas_test?sslmode=disable" \
    golang:1.25-alpine sh -c 'go test -tags=integration ./...' || rc=1
  docker rm -f "$PG" >/dev/null 2>&1 || true
  return "$rc"
}

validate_frontend() {
  local fe="$SRC/pandora-loan-helper-frontend"
  log "validando frontend: lint/typecheck/test"
  docker run --rm -v "$fe:/app" -w /app -v atlas-build-npmcache:/root/.npm \
    node:20-alpine sh -c 'npm ci --no-audit --no-fund && npm run lint && npm run typecheck && npm test'
}

# --------------------------------------------------------------------------------------------------

sync_repo "$BACKEND_REPO" "$SRC/atlas-go"
sync_repo "$FRONTEND_REPO" "$SRC/pandora-loan-helper-frontend"

b_sha="$(git -C "$SRC/atlas-go" rev-parse "origin/$BRANCH")"
f_sha="$(git -C "$SRC/pandora-loan-helper-frontend" rev-parse "origin/$BRANCH")"
pair="$b_sha $f_sha"

if [ -f "$STATE" ] && [ "$(cat "$STATE")" = "$pair" ]; then
  log "sem mudanca (backend ${b_sha:0:9}, frontend ${f_sha:0:9})"
  exit 0
fi
if [ -f "$FAILED" ] && [ "$(cat "$FAILED")" = "$pair" ]; then
  log "esse par de commits ja falhou na validacao — aguardando commit novo"
  exit 0
fi

git -C "$SRC/atlas-go" checkout --quiet --force "$b_sha"
git -C "$SRC/pandora-loan-helper-frontend" checkout --quiet --force "$f_sha"

log "validando backend ${b_sha:0:9} + frontend ${f_sha:0:9}"
docker network create "$NET" >/dev/null 2>&1 || true

if ! validate_backend; then
  printf '%s' "$pair" > "$FAILED"
  log "VALIDACAO DO BACKEND FALHOU — nao publica (corrija e faca novo merge)"
  exit 0
fi
if ! validate_frontend; then
  printf '%s' "$pair" > "$FAILED"
  log "VALIDACAO DO FRONTEND FALHOU — nao publica (corrija e faca novo merge)"
  exit 0
fi
rm -f "$FAILED"

log "validacao OK — buildando imagem"
printf '%s' "$GH_TOKEN" | docker login ghcr.io -u "$GHCR_USER" --password-stdin >/dev/null

# O Dockerfile espera o backend e o frontend como pastas IRMAS dentro do contexto ($SRC).
docker build \
  --pull \
  --build-arg "BACKEND_VERSION=$b_sha" \
  -t "$IMAGE:latest" \
  -t "$IMAGE:$b_sha" \
  -f "$SRC/atlas-go/Dockerfile" \
  "$SRC"

docker push --quiet "$IMAGE:$b_sha"
docker push --quiet "$IMAGE:latest"

printf '%s' "$pair" > "$STATE"
log "publicado $IMAGE:$b_sha (:latest) — atlas-auto-update aplica em ate 5 min"
