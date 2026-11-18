#!/usr/bin/env bash
# Mede a cobertura de testes do backend e aplica a trava de cobertura.
#
# Soma três fontes num relatório só:
#   1. testes de unidade e de integração (go test, com TEST_DATABASE_URL);
#   2. testes E2E (e2e/, com E2E_POSTGRES_URL), que sobem a API compilada com -cover.
#
# Regras (falha com exit 1 se alguma quebrar):
#   - a cobertura total não pode cair abaixo do piso em .coverage-baseline (só sobe, nunca desce);
#   - todo pacote listado em .coverage-strict precisa ter pelo menos 80%.
#
# Uso:
#   TEST_DATABASE_URL=... E2E_POSTGRES_URL=... scripts/coverage.sh
#   scripts/coverage.sh --update-baseline   # sobe o piso para a cobertura atual
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
OUT="${COVERAGE_OUT:-$ROOT/coverage}"
META=80
rm -rf "$OUT"; mkdir -p "$OUT/e2e-raw"

: "${TEST_DATABASE_URL:?defina TEST_DATABASE_URL (veja scripts/prepare-test-db.sh)}"
: "${E2E_POSTGRES_URL:?defina E2E_POSTGRES_URL}"

echo "==> testes de unidade e integração"
go test -count=1 -p 1 -covermode=set -coverpkg=./internal/... -coverprofile="$OUT/unit.out" ./... >"$OUT/unit.log" 2>&1 \
  || { cat "$OUT/unit.log"; exit 1; }

echo "==> testes E2E"
E2E_COVERDIR="$OUT/e2e-raw" go test -tags e2e -count=1 ./e2e/... >"$OUT/e2e.log" 2>&1 \
  || { cat "$OUT/e2e.log"; exit 1; }
go tool covdata textfmt -i="$OUT/e2e-raw" -o "$OUT/e2e.out"

# Junta os dois perfis: um bloco de código conta como coberto se qualquer fonte o executou.
{
  echo "mode: set"
  tail -q -n +2 "$OUT/unit.out" "$OUT/e2e.out" \
    | awk '{ if (!($1 in n)) { n[$1]=$2; c[$1]=0 } if ($3 > 0) c[$1]=1 } END { for (k in n) print k, n[k], c[k] }' \
    | sort
} >"$OUT/total.out"

# Cobertura por pacote e total, contando statements.
awk 'NR > 1 {
  split($1, parte, ":"); arquivo = parte[1]
  pacote = arquivo; sub(/\/[^\/]*$/, "", pacote); sub(/^github.com\/atlas\/atlas-go\//, "", pacote)
  total[pacote] += $2; if ($3 > 0) cobertos[pacote] += $2
  T += $2; if ($3 > 0) C += $2
} END {
  for (p in total) if (total[p] > 0) printf "%6.1f%% %s\n", 100 * cobertos[p] / total[p], p
  printf "%6.1f%% TOTAL\n", 100 * C / T
}' "$OUT/total.out" | sort -k2 >"$OUT/summary.txt"

TOTAL="$(awk '$2 == "TOTAL" { sub(/%/, "", $1); print $1 }' "$OUT/summary.txt")"
BASELINE="$(tr -d '[:space:]' < .coverage-baseline)"

echo
echo "==> cobertura por pacote"
grep -v ' TOTAL$' "$OUT/summary.txt" | sort -n
echo
echo "Cobertura total: ${TOTAL}% (piso atual: ${BASELINE}%, meta: ${META}%)"

if [ "${1:-}" = "--update-baseline" ]; then
  echo "$TOTAL" > .coverage-baseline
  echo "Piso atualizado para ${TOTAL}%."
fi

FALHOU=0
if awk -v t="$TOTAL" -v b="$BASELINE" 'BEGIN { exit !(t + 0.05 < b) }'; then
  echo "ERRO: a cobertura total caiu de ${BASELINE}% para ${TOTAL}%." >&2
  FALHOU=1
fi

while read -r pacote; do
  case "$pacote" in ''|\#*) continue;; esac
  valor="$(awk -v p="$pacote" '$2 == p { sub(/%/, "", $1); print $1 }' "$OUT/summary.txt")"
  if [ -z "$valor" ]; then
    echo "ERRO: o pacote $pacote está em .coverage-strict mas não apareceu no relatório." >&2
    FALHOU=1
  elif awk -v v="$valor" -v m="$META" 'BEGIN { exit !(v < m) }'; then
    echo "ERRO: $pacote está com ${valor}% (mínimo ${META}%)." >&2
    FALHOU=1
  fi
done < .coverage-strict

exit "$FALHOU"
