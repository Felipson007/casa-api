-- ============================================================================================
-- read_only marks a calendar event as Atlas-owned (derived from another module: loan instalment,
-- birthday, document expiration). The sync pushes these to the provider but never pulls edits or
-- deletions back — the source of truth is the Atlas module, so an event edited or deleted on the
-- phone is recreated on the next sync.
-- ============================================================================================
ALTER TABLE calendar_events ADD COLUMN read_only boolean NOT NULL DEFAULT false;
