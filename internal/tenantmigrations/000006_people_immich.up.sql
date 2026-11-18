-- Adds the column 000004 deliberately deferred ("photo_path/immich_person_id are deliberately NOT
-- restored here — that's the photo/Immich integration, a separate, larger, still out-of-scope
-- feature"). Mirrors the old Java V56__pessoa_immich.sql: pessoas.immich_person_id varchar(100) —
-- a manual link the household sets once via the person edit form (Immich's own IDs are UUIDs, but
-- stored as free text here, same as Java, since nothing parses/validates it as a UUID).
ALTER TABLE people
    ADD COLUMN immich_person_id varchar(100);
