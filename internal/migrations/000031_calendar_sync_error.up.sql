-- ============================================================================================
-- calendar_events.sync_error / sync_failed_at — remembers the last push failure for an event so
-- the sync stops retrying it on every single run. Before this, a PUT that failed (e.g. 403 on a
-- read-only/shared iCloud calendar) never persisted external_id, so the event fell into the same
-- "create" branch on the next sync and retried forever, identical error every time. The UI can now
-- surface sync_error instead of silently re-attempting; sync_failed_at lets a future retry policy
-- back off (e.g. only retry after N hours), though no such policy exists yet — clearing the column
-- happens on the next successful push (see calendarsync.syncPush).
-- ============================================================================================
ALTER TABLE calendar_events
    ADD COLUMN sync_error varchar(500),
    ADD COLUMN sync_failed_at timestamptz;
