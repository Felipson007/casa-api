# app-golang-atlas (atlas-go)

Go rewrite of the **Pandora Finance** personal finance / loan-management backend, replacing the
original Java/Spring app (`pandora-loan-helper`). Domain-driven, layered architecture with a
PostgreSQL persistence layer and async jobs.

> **Status:** personal/homelab use — no tenant isolation (single-owner by design). See
> `docs/architecture-plan.md` for the full implementation handoff.

## Features

- **Finance & loans** — income, expenses, categories, loans, people, pets, documents, reports
- **Auth** — Google OAuth2 (cookie session) + password reset
- **Dashboards & audit** — spending/income dashboards, full audit trail
- **Documents** — PDF generation (`fpdf`), Excel export (`excelize`), PDF parsing (`ledongthuc/pdf`)
- **Async jobs** — background workers (`cmd/worker`), backups encrypted with `age`
- **Chat/integrations** — pluggable external integrations with encrypted refresh tokens

## Tech Stack

- **Language:** Go 1.25.0 (module `github.com/atlas/atlas-go`)
- **Database:** PostgreSQL via `pgx/v5`
- **Crypto:** `golang.org/x/crypto`, `age` (backup encryption)
- **Validation:** `go-playground/validator/v10`
- **Tooling:** Docker + Docker Compose (homelab)

## Project Layout

```
cmd/            entrypoints (server, worker)
internal/       domain-driven packages (loans, finance, auth, chat, ...)
config/         runtime config
docs/           architecture-plan.md, audits, deployment guide
scripts/        ops helpers
homelab/        homelab deployment assets
```

## Running (Docker Compose)

```bash
cp .env.example .env          # fill DB_PASSWORD, GOOGLE_*, APP_ENCRYPTION_KEY, APP_BACKUP_AGE_RECIPIENT
docker compose -f docker-compose.homelab.yml up -d
```

## Configuration (`.env`)

| Variable | Purpose |
| --- | --- |
| `DB_PASSWORD` | Postgres password (use `openssl rand -hex 24`, URL-safe) |
| `APP_FRONTEND_ORIGIN` | OAuth redirect origin (e.g. `https://atlas-server.taile…ts.net`) |
| `APP_ENCRYPTION_KEY` | 32-byte base64 key for integration refresh tokens |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | Google OAuth credentials |
| `APP_BACKUP_AGE_RECIPIENT` | `age1…` public key for encrypted backups (private key stays offline) |

## Documentation

- `docs/architecture-plan.md` — approved 14-section outline
- `docs/homelab-deploy.md` — deploy do homelab (k3s + ArgoCD)
- `docs/audit-*.md` — audit reports

## Related

- Frontend: [`app-frontend-atlas`](https://github.com/gustavodvp/app-frontend-atlas)
