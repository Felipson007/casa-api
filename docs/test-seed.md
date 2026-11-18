# Test seed — restoring production-shaped data for integration tests

`testdata/seed.sql` is a **fixture**: the new single-tenant schema (migration `000001`) populated
with the user's real production data, translated per `docs/schema-migration-map.md`. It is generated
once by `scripts/etl/build-seed.sh` (which reads `testdata/prod-dump.sql`, the raw production dump,
and runs `scripts/etl/transform.sql`). Tests restore `seed.sql` — they never touch the production
homelab and never re-run the ETL.

## What is in the repo

| file | purpose |
| --- | --- |
| `testdata/prod-dump.sql` | raw production dump (pt, multi-tenant, `MAE_DE`, `timestamp`). Reference only — NOT restorable into the new schema directly. Lets any AI inspect "how production looks" without SSH to the homelab. |
| `testdata/seed.sql` | the converted fixture (en, single-tenant, `MOTHER_OF`, `timestamptz`, `gender`). Restored by tests. |
| `scripts/etl/build-seed.sh` | regenerates `seed.sql` from `prod-dump.sql` (one-shot ETL). |
| `scripts/etl/transform.sql` | legacy→new INSERT/SELECT with all enum translations and FK-orphan safety. |
| `docs/schema-migration-map.md` | the old→new mapping (single source of truth for the ETL). |

## Restoring seed.sql for a test run

The seed is a **data-only** dump; the schema must already exist (apply migration `000001` first).
Because `categories` has a circular self-FK, load with FK checks disabled:

```bash
# 1. start a throwaway Postgres
docker run -d --name atlas-test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=atlas -p 55450:5432 postgres:16
sleep 6

# 2. apply the single migration
docker exec -i atlas-test psql -U postgres -d atlas -v ON_ERROR_STOP=1 \
  < internal/migrations/000001_init_schema.up.sql

# 3. load the fixture with FK checks off (circular categories self-FK)
docker exec -i atlas-test psql -U postgres -d atlas -c "SET session_replication_role = replica;"
docker exec -i atlas-test psql -U postgres -d atlas < testdata/seed.sql

# 4. run integration tests (they use TEST_DATABASE_URL=postgres://postgres:test@localhost:55450/atlas)
go test -tags=integration ./...

# 5. cleanup
docker rm -f atlas-test
```

## Regenerating seed.sql (only when production data changes)

```bash
bash scripts/etl/build-seed.sh          # outputs testdata/seed.sql
```

If new enum values appear in production, first inspect them against the mirror DB
(`docker exec -i atlas-prod-mirror psql -U postgres -d pandora_prod -c "SELECT ... DISTINCT ..."`),
then extend the CASE mappings in `scripts/etl/transform.sql` — do NOT guess, always SELECT the real
values first (the production mirror runs locally on :5432 as `atlas-prod-mirror`).

## Validation status

Row counts match production exactly across all 21 mapped tables. Enum translations verified:
`MOTHER_OF/FATHER_OF/SPOUSE_OF`, `FIXED` payment type, `ACTIVE/PAID_OFF/CANCELLED` loan status,
`PAID/PENDING` installment status, `IMMICH/TELEGRAM/OPENAI/OTHER`, `FGTS/PRIVATE_PENSION/CHECKING`,
`USER/ASSISTANT` chat role, `OWNER/MEMBER` user role. `gender` derived from `parentesco` + parent
edges; 0 women as `FATHER_OF` and 0 men as `MOTHER_OF` (consistent with the people-service rule).
