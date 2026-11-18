-- ============================================================================================
-- person_relationships_unique — closes a race in CreateRelationship: the service does an
-- application-level duplicate check before inserting, but two concurrent identical requests can
-- both pass that check and insert the same edge twice. A unique index on the edge (from, to, type)
-- makes the DB the actual source of truth for "no duplicate edges", with the existing
-- pgErr.Code == "23505" handling in Repository.CreateRelationship translating the violation into
-- people.ErrRelationshipExists.
-- ============================================================================================
CREATE UNIQUE INDEX idx_person_relationships_unique_edge
    ON person_relationships (from_person_id, to_person_id, type);
