-- ============================================================================================
-- calendar_events — the first user-owned calendar entity. Until now the Atlas calendar was
-- entirely derived at read time (loan installments, birthdays, document expirations — see
-- internal/dashboard/calendar.go), so there was nowhere to store an event the owner actually
-- typed. This is the foundation for creating events in the app and, in later slices, syncing
-- them with Google Calendar and iCloud (CalDAV).
--
-- Shape:
--   * all_day events occupy [start_date, end_date] (end_date NULL = single day) and carry no times.
--   * timed events carry start_time and an optional end_time ("HH:MM").
--   * source = MANUAL today; GOOGLE/ICLOUD are reserved for the sync slices and are how a row is
--     matched to its external counterpart (external_provider + external_id).
--   * deleted_at is a soft delete so a future sync can propagate the removal outward instead of
--     silently dropping the mapping; reads always filter it out.
-- ============================================================================================
CREATE TABLE calendar_events (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title                varchar(200) NOT NULL,
    description          text,
    location             varchar(200),
    all_day              boolean NOT NULL DEFAULT true,
    start_date           date NOT NULL,
    end_date             date,
    start_time           time,
    end_time             time,
    source               varchar(20) NOT NULL DEFAULT 'MANUAL',
    external_provider    varchar(20),
    external_id          varchar(255),
    external_calendar_id varchar(255),
    etag                 varchar(255),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    deleted_at           timestamptz,
    CONSTRAINT ck_calendar_events_source CHECK (source IN ('MANUAL', 'GOOGLE', 'ICLOUD')),
    CONSTRAINT ck_calendar_events_all_day_times CHECK (
        (all_day AND start_time IS NULL AND end_time IS NULL)
        OR (NOT all_day AND start_time IS NOT NULL)
    ),
    CONSTRAINT ck_calendar_events_range CHECK (end_date IS NULL OR end_date >= start_date)
);

-- Range scans always ask "which events overlap [from, to]" and only ever see live rows.
CREATE INDEX idx_calendar_events_range ON calendar_events (start_date, end_date) WHERE deleted_at IS NULL;

-- One row per external event once sync exists (partial: manual rows have no external_id).
CREATE UNIQUE INDEX uq_calendar_events_external ON calendar_events (external_provider, external_id) WHERE external_id IS NOT NULL;
