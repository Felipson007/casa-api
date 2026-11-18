# Endpoint Integration Test Report — 2026-08-09 (Hermes sweep)

## Method
Ran the real API server (`cmd/api`) against `atlas-test-db` (Postgres 16, schema 000001 + 000002 +
seed) on `:8080`. Hit every endpoint with `urllib` as the frontend would: OWNER token
(`guzahard@gmail.com`), MEMBER token (`sanches57@uni9.edu.br`, seeded with `member_permissions`
on `expenses` only per D-012), and no token. Scripts: `scripts/test_endpoints_smoke.py`,
`scripts/test_endpoints_full.py`.

## Bugs found (all fixed or reported)

### B1 — CRITICAL: MEMBER requests panic (500) on every module  [FIXED, commit 84128ad]
`cmd/api/main.go` set `httpx.DefaultCheckPerm = checkPerm` (line ~173) **after** the
`RegisterRoutes(...)` calls (lines ~152-166). `RequirePerm(httpx.DefaultCheckPerm, ...)` captures the
value at RegisterRoutes time, so every module registered `check == nil`. OWNER bypasses the check and
worked; MEMBER called `nil(ctx, ...)` → nil-pointer panic → HTTP 500 on **every** MEMBER request.
Fix: moved `auth.New(pool)` + `httpx.DefaultCheckPerm = checkPerm` to **before** `mux := ...`.
Repro: `GET /api/expenses` as MEMBER → 500; as OWNER → 200.

### B2 — people: NULL gender scan  [FIXED, commit 84128ad]
`internal/people/postgres/repository.go` `ListPeople`/`FindPerson` scanned `gender` (column is
`varchar(1)` with **no NOT NULL**) into a `string`, failing with `cannot scan NULL into *string` →
HTTP 500 on `GET /api/people`. Fix: scan into `*string`, call `GenderOf` only when non-nil.

### B3 — /health requires auth  [FINDING, not yet fixed]
`AuthMiddleware` wraps the whole `mux`, so `GET /health` returns 401 without a token. A healthcheck
should be public (dashboard/frontend liveness without credentials). Options: register `/health`
outside the middleware, or let the middleware skip `/health`. Awaiting Claude's call.

### B4 — loans part 2 breaks the build  [BLOCKER, Claude's WIP]
`loans.Repository` gained `ApplyAmortization` (D-011 part 2, in progress by Claude) but
`internal/loans/postgres/repository.go` does not implement it yet → `go build ./cmd/api` fails.
Hermes is **not** touching Claude's WIP; sweep will re-run once loans part 2 compiles.

## Endpoint results (post-fix, pre-loans-WIP)

| Module | OWNER GET | MEMBER GET | Notes |
|---|---|---|---|
| categories | 200 | 403 (no perm) | correct |
| expenses | 200 | 200 (has perm) | correct; POST 201 |
| people | 200 | 200 | B2 fixed |
| relationships | 200 | n/a | path is `/api/relationships` (not `/api/person-relations`) |
| clients | 200 | n/a | |
| loans | 200 | n/a | list only; part 2 pending |
| income | 200 | n/a | |
| assets | 200 | n/a | |
| documents | 200 | n/a | |
| pets | 200 | n/a | |
| health/markers | 200 | n/a | B3: 401 without token |
| notifications | 200 | n/a | |
| chat/messages | 200 | n/a | per-user scope verified |
| audit-logs | 200 | n/a | append-only verified |
| finance/balances | 200 | n/a | snapshot side-effect verified (1000==1000.00 → 0 history; 1000→2000 → 1) |
| integrations | (wired) | n/a | needs APP_ENCRYPTION_KEY at Connect time |

## Auth matrix (correct after B1 fix)
- no token → 401 (all routes)
- bad token → 401
- OWNER → bypass, 200/201/204
- MEMBER + `member_permissions` row → 200/201/204
- MEMBER without row → 403

## Next steps
1. Claude finishes loans part 2 (B4) → rebuild server → re-run full sweep.
2. Decide B3 (/health public) and apply.
3. Expand sweep to cover POST/PUT/DELETE on every module (currently only smoke-covered for a few).
