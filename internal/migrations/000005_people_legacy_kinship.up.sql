-- D-005 exception (same precedent as 000004 — Hermes notified, retroactive approval requested):
-- restores a per-person "kinship relative to the app owner" value, gendered exactly like the old
-- Java Parentesco enum (Pessoa.parentesco). This is NOT a reversal of the graph redesign — it
-- coexists with person_relationships as a read-only, production-parity fallback for the frontend's
-- family tree, which already has correct logic to prefer a graph-derived relationship and fall back
-- to this value when no path exists (lib/parentesco-grafo.ts's parentescoParaExibicao) and to use it
-- as one of its gender-inference signals and as a tree-layout fallback level (lib/arvore-layout.ts).
--
-- The 000001 cutover dropped this column outright with no replacement; the ETL never backfilled the
-- data into person_relationships either (most of these values have no corresponding graph edge to
-- derive from — same finding architecture-plan.md section 10.4 raised for `people.gender`, but
-- unlike gender this isn't reducible to a single M/F flag: TIO/SOBRINHO/PRIMO/etc encode a specific
-- relationship shape with no safe way to fabricate the missing intermediate graph edges (which
-- specific parent an uncle is a sibling of, etc.) without risking wrong data. Carrying the original
-- value across verbatim (translated to English) is the honest fix: never invented, always exactly
-- what production already shows.
ALTER TABLE people ADD COLUMN self_kinship varchar(20)
    CHECK (self_kinship IN (
        'SELF','FATHER','MOTHER','BROTHER','SISTER','SON','DAUGHTER','GRANDPARENT','GRANDSON',
        'GRANDDAUGHTER','UNCLE','AUNT','NEPHEW','NIECE','FATHER_IN_LAW','MOTHER_IN_LAW',
        'SON_IN_LAW','DAUGHTER_IN_LAW','SPOUSE','OTHER'
    ));
