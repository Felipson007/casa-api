-- Restores columns the old pessoas table had that 000001 dropped without a replacement: nickname
-- (apelido), birth_date (data_nascimento), notes (observacoes), active (ativo), email. Found
-- missing while wiring the real frontend against this backend — PessoaDto and the health-profile
-- dashboard card both need them. photo_path/immich_person_id are deliberately NOT restored here —
-- that's the photo/Immich integration, a separate, larger, still out-of-scope feature (see the
-- endpoint atlas's "Missing" bucket).
ALTER TABLE people
    ADD COLUMN nickname   varchar(200),
    ADD COLUMN birth_date date,
    ADD COLUMN notes      varchar(2000),
    ADD COLUMN active     boolean NOT NULL DEFAULT true,
    ADD COLUMN email      varchar(255);
