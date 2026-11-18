# Real-data comparison audit — 2026-08-10 (Claude, solo pass)

## Context
Hermes is out of token budget; per the user's direction, finishing all remaining work and
validating it against real production data falls to a single agent from here. This note covers
the first real-data comparison pass: booting the Go API against a **clean** restore of
`testdata/seed.sql` (the real production data, translated per
`docs/schema-migration-map.md`) and cross-checking behavior directly against `atlas-prod-mirror`
(the raw old-schema copy) and `testdata/prod-dump.sql`.

## Method
1. `atlas-test-db` had accumulated pollution from repeated `scripts/sweep_full.py` runs (duplicate
   "Sweep Person"/"Sweep Client"/"SweepPet" rows — the script only cleans up categories and finance
   balances, not people/clients/pets/loans it creates). Recreated it from scratch: migration 000001
   + 000002, then `testdata/seed.sql`.
2. Verified row counts for all 21 mapped tables match `testdata/prod-dump.sql` / `atlas-prod-mirror`
   **exactly** (clients 12, people 26, loans 22, installments 111, loan_transactions 49, expenses
   882, income_entries 116, assets 6, pets 4, documents 32, person_relationships 39, categories 36,
   ...). Confirms `docs/test-seed.md`'s claim still holds after the modules added since it was
   written (chat, integrations, finance, backup, settings, passwordreset).
3. Booted `cmd/api` against the clean seed, hit real endpoints with the real OWNER user ID, and
   cross-checked responses against direct SQL on both databases.

## Confirmed correct (real data, not just seed fixtures)
- Financial totals match exactly, old vs. new: `sum(gastos.valor)` = `sum(expenses.amount)` =
  55364.80; `sum(receitas.valor)` = `sum(income_entries.amount)` = 97849.20;
  `sum(emprestimos.saldo_devedor)` = `sum(loans.outstanding_balance)` = 33200.00.
- Spot-checked a specific loan (`85b5abf1-...`, client Elizangela): `saldo_devedor` 7500.00 /
  `status` ATIVO / `tipo_pagamento` UNICO in the old DB exactly match `outstanding_balance` 7500.00
  / `status` ACTIVE / `payment_type` RECURRING_INTEREST via the live Go API — confirms both the
  enum translation (`ATIVO`→`ACTIVE`, `UNICO`→`RECURRING_INTEREST`) and the numeric/precision
  round-trip (`taxa_juros_mensal` 16.20 → `monthly_interest_rate` 16.200000) are correct end to end.
- `tipo_pagamento`'s `UNICO`→`RECURRING_INTEREST` / `PARCELADO`→`INSTALLMENT` translation is
  implemented correctly in `scripts/etl/transform.sql` but was missing from the enum-translation
  table in `docs/schema-migration-map.md` — doc gap only, not a behavior gap; add it if that file
  is revised.

## Bug found and fixed: `internal/personrelations.DeriveFromOrigin` (kinship derivation)

Only surfaced by testing against the real family graph (real data has topologically deep,
multi-branch relationships — siblings with spouses, remarriage, etc. — that no synthetic fixture
had exercised). Hitting `GET /api/people/{realPersonId}/relationships/derived` for the real "self"
person returned a pile of ~9 entries all labeled `INLAW_FATHER`, including at least one (`IZABEL`,
recorded as `MOTHER_OF` several people — unambiguously female) that was obviously wrong.

Root causes (both in `internal/personrelations/derive.go`, pre-fix):
1. **Gender was never wired in at all.** The doc comments referenced a `genderOf`/gender-aware
   design, but `DeriveFromOrigin`'s actual signature took no gender input, and the caller
   (`people.Service.DeriveFromOrigin`) never fetched or passed genders. Every gendered term
   (uncle/aunt, nephew/niece, in-law parent, in-law child) silently used a hardcoded male-leaning
   branch.
2. **Path matching was by category *count*, not exact sequence.** `termoPorCaminho` classified a
   path by counting SOBE/DESCE/LADO steps and checking pairs of counts (e.g.
   `ladoCount==1 && sobeCount==1`) without requiring the third count to be zero. A 3-step path like
   a sibling's spouse (SOBE-DESCE-LADO: one of each) satisfies `ladoCount==1 && sobeCount==1` just
   as much as the true 2-step "spouse's parent" (LADO-SOBE) does, and the switch's case order meant
   the former always matched the latter's branch first. This is exactly why every in-law reached
   *through* a sibling collapsed into `INLAW_FATHER`.

Both are deviations from the reference implementation
(`../pandora-loan-helper-frontend/lib/parentesco-grafo.ts`), which matches the exact normalized
path string (not counts) and resolves gender per-target before selecting a term.

**Fix** (`internal/personrelations/derive.go`, `internal/people/repository.go`,
`internal/people/service.go`):
- `DeriveFromOrigin` and `termoPorCaminho` now take a `genders map[uuid.UUID]Gender` and match on
  the exact joined path key (`pathKey`), covering all cases the TS table covers, in the same
  precedence, including the two free-label (`RotuloLivre`) cases the Go port had never emitted at
  all — step-parent (`Padrasto`/`Madrasta`, SOBE-LADO) and sibling-in-law (`Cunhado`/`Cunhada`,
  SOBE-DESCE-LADO / LADO-SOBE-DESCE). An unrecognized path now correctly produces no entry (matches
  "melhor nenhum rótulo que um errado" in the TS source) instead of being force-fit into the
  nearest-shaped bucket.
- `people.Service.DeriveFromOrigin` now also loads `ListPeople` to build the gender map (one extra
  O(1) round trip, not a loop) and passes it through. Since the new schema stores `people.gender`
  explicitly (no `parentesco` column), this is actually simpler than the TS cascade heuristic
  (`generoDeId`) it replaces — a missing/unknown gender defaults to male, same fallback the TS code
  used.
- Added `internal/personrelations/derive_test.go` (previously zero test coverage on this package)
  with regression cases for both root causes plus the unmatched-path and default-gender behaviors.

**Verified against the real graph, post-fix:** the same real "self" person's derived list went
from ~9 misclassified `INLAW_FATHER` entries to a correct, gender-split distribution — `SIBLING`
×3, `PARENT` ×2, `NEPHEW`/`NIECE` ×2 each, `SPOUSE`, `SELF`, `INLAW_MOTHER` ×1, `INLAW_FATHER` ×1,
free-label `Cunhado` ×9 (siblings' spouses), free-label `Padrasto` ×1 — matching what the reference
TS logic would produce for this same graph.

## Not yet done
- `internal/people` still has zero test files beyond the new `derive_test.go` in
  `personrelations` — `CreateRelationship`'s gender-mismatch/cycle checks and `DeriveFromOrigin`'s
  wiring are only covered indirectly via this manual real-data pass, not via `go test`.
- `sweep_full.py` should get a teardown step for the people/clients/pets/loans it creates (or move
  to a throwaway per-run schema) — repeated runs against `atlas-test-db` silently accumulate rows,
  which is what made this same container unusable for a byte-for-byte real-data comparison until
  it was recreated from scratch.
- The remainder of the endpoint-by-endpoint real-data comparison (documents, health, notifications,
  chat, integrations, finance, backup, settings, passwordreset) has not yet been spot-checked
  against real values the way loans/expenses/income/categories/people were in this pass.
