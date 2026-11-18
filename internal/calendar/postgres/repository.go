// Package postgres implements calendar.Repository — the only file in the calendar module that
// imports a SQL driver. Every query goes through db.WithTx. time columns are read as ::text and
// normalized to "HH:MM" in Go, so the module never depends on a pgx time codec.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/calendar"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ calendar.Repository = (*Repository)(nil)

const selectEvent = `
	SELECT e.id, e.title, e.description, e.location, e.all_day, e.start_date, e.end_date,
	       e.start_time::text, e.end_time::text, e.source, e.read_only,
	       e.external_provider, e.external_id, e.external_calendar_id, e.external_href, e.etag,
	       e.external_synced_at, e.sync_error, e.sync_failed_at, e.created_at, e.updated_at, e.deleted_at
	FROM calendar_events e`

func (r *Repository) ListRange(ctx context.Context, from, to time.Time) ([]calendar.Event, error) {
	var result []calendar.Event
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, selectEvent+`
			WHERE e.deleted_at IS NULL
			  AND e.read_only = false
			  AND e.start_date <= $2
			  AND COALESCE(e.end_date, e.start_date) >= $1
			ORDER BY e.start_date, e.start_time NULLS FIRST, e.title`, from, to)
		if err != nil {
			return fmt.Errorf("querying calendar_events: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEvent(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*calendar.Event, error) {
	var result *calendar.Event
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		e, err := scanEvent(tx.QueryRow(ctx, selectEvent+` WHERE e.id = $1 AND e.deleted_at IS NULL`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &e
		return nil
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, e calendar.Event) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return insertEvent(ctx, tx, e)
	})
}

func (r *Repository) Update(ctx context.Context, e calendar.Event) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return updateEvent(ctx, tx, e, false)
	})
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.SoftDelete(ctx, id)
}

// --- sync surface (internal/icloudcal) -------------------------------------------------------

// ListLive returns live events overlapping [from, to] that belong to this provider: never-synced
// rows (external_provider NULL) or rows already mapped to it. Rows mapped to a DIFFERENT provider
// are excluded on purpose, so switching the sync target away and back never makes one provider
// delete another provider's events.
func (r *Repository) ListLive(ctx context.Context, provider string, from, to time.Time) ([]calendar.Event, error) {
	var result []calendar.Event
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, selectEvent+`
			WHERE e.deleted_at IS NULL
			  AND e.start_date <= $3
			  AND COALESCE(e.end_date, e.start_date) >= $2
			  AND (e.external_provider IS NULL OR e.external_provider = $1)
			ORDER BY e.start_date, e.start_time NULLS FIRST, e.title`, provider, from, to)
		if err != nil {
			return fmt.Errorf("querying calendar_events for sync: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEvent(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

// ListDeletedExternal returns soft-deleted rows that still carry an external mapping, so the sync
// can propagate the removal to the provider before dropping the mapping.
func (r *Repository) ListDeletedExternal(ctx context.Context, provider string) ([]calendar.Event, error) {
	var result []calendar.Event
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, selectEvent+`
			WHERE e.deleted_at IS NOT NULL
			  AND e.external_provider = $1
			  AND e.external_id IS NOT NULL`, provider)
		if err != nil {
			return fmt.Errorf("querying deleted calendar_events: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEvent(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

// InsertSynced inserts a row pulled from the provider.
func (r *Repository) InsertSynced(ctx context.Context, e calendar.Event) error {
	return r.Create(ctx, e)
}

// UpdateSynced writes content + external mapping and stamps external_synced_at, marking the row
// reconciled so it is not seen as locally dirty on the next pass.
func (r *Repository) UpdateSynced(ctx context.Context, e calendar.Event) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return updateEvent(ctx, tx, e, true)
	})
}

// SoftDelete marks a row deleted (propagating a remote deletion), leaving it for the external
// mapping to be cleared separately.
func (r *Repository) SoftDelete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE calendar_events SET deleted_at = now(), updated_at = now()
			WHERE id = $1 AND deleted_at IS NULL`, id)
		if err != nil {
			return fmt.Errorf("deleting calendar_event: %w", err)
		}
		return nil
	})
}

// MarkPushFailed records a push failure without touching content/mapping fields, so
// calendarsync.Sync can skip retrying the same event on every run — see pushRetryGap.
func (r *Repository) MarkPushFailed(ctx context.Context, id uuid.UUID, cause string, failedAt time.Time) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE calendar_events SET sync_error = $2, sync_failed_at = $3
			WHERE id = $1`, id, cause, failedAt)
		if err != nil {
			return fmt.Errorf("marking calendar_event push failed: %w", err)
		}
		return nil
	})
}

// ClearExternal drops the external mapping after the remote resource has been removed, so the
// soft-deleted row no longer shows up as pending.
func (r *Repository) ClearExternal(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE calendar_events
			SET external_provider = NULL, external_id = NULL, external_calendar_id = NULL,
			    external_href = NULL, etag = NULL, external_synced_at = NULL, updated_at = now()
			WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("clearing calendar_event external mapping: %w", err)
		}
		return nil
	})
}

// UpsertDerived materializes an Atlas-owned event. The UPDATE is guarded by a change check so a
// no-op reconciliation does not bump updated_at (which would otherwise mark the row dirty and make
// the sync re-push it every run). A previously soft-deleted row is revived.
func (r *Repository) UpsertDerived(ctx context.Context, e calendar.Event) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO calendar_events (id, title, description, all_day, start_date, source, read_only)
			VALUES ($1, $2, $3, true, $4, $5, true)
			ON CONFLICT (id) DO UPDATE SET
				title = EXCLUDED.title, description = EXCLUDED.description,
				all_day = EXCLUDED.all_day, start_date = EXCLUDED.start_date,
				source = EXCLUDED.source, read_only = true, deleted_at = NULL, updated_at = now()
			WHERE calendar_events.title IS DISTINCT FROM EXCLUDED.title
			   OR calendar_events.description IS DISTINCT FROM EXCLUDED.description
			   OR calendar_events.start_date IS DISTINCT FROM EXCLUDED.start_date
			   OR calendar_events.deleted_at IS NOT NULL`,
			e.ID, e.Title, e.Description, e.StartDate, e.Source)
		if err != nil {
			return fmt.Errorf("upserting derived calendar_event: %w", err)
		}
		return nil
	})
}

func (r *Repository) SoftDeleteReadOnlyNotIn(ctx context.Context, keep []uuid.UUID) error {
	ids := make([]string, len(keep))
	for i, id := range keep {
		ids[i] = id.String()
	}
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE calendar_events SET deleted_at = now(), updated_at = now()
			WHERE read_only = true AND deleted_at IS NULL AND id <> ALL($1::uuid[])`, ids)
		if err != nil {
			return fmt.Errorf("pruning derived calendar_events: %w", err)
		}
		return nil
	})
}

func insertEvent(ctx context.Context, tx pgx.Tx, e calendar.Event) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO calendar_events
		  (id, title, description, location, all_day, start_date, end_date, start_time, end_time,
		   source, read_only, external_provider, external_id, external_calendar_id, external_href,
		   etag, external_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::time, $9::time, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT (external_provider, external_id) WHERE external_id IS NOT NULL DO UPDATE SET
			title = EXCLUDED.title, description = EXCLUDED.description, location = EXCLUDED.location,
			all_day = EXCLUDED.all_day, start_date = EXCLUDED.start_date, end_date = EXCLUDED.end_date,
			start_time = EXCLUDED.start_time, end_time = EXCLUDED.end_time,
			external_calendar_id = EXCLUDED.external_calendar_id, external_href = EXCLUDED.external_href,
			etag = EXCLUDED.etag, external_synced_at = EXCLUDED.external_synced_at,
			deleted_at = NULL, updated_at = now()
	`, e.ID, e.Title, e.Description, e.Location, e.AllDay, e.StartDate, e.EndDate,
		e.StartTime, e.EndTime, e.Source, e.ReadOnly, e.ExternalProvider, e.ExternalID,
		e.ExternalCalendarID, e.ExternalHref, e.ETag, e.ExternalSyncedAt)
	if err != nil {
		return fmt.Errorf("inserting calendar_event: %w", err)
	}
	return nil
}

func updateEvent(ctx context.Context, tx pgx.Tx, e calendar.Event, stampSynced bool) error {
	syncedAt := "external_synced_at"
	args := []any{e.ID, e.Title, e.Description, e.Location, e.AllDay, e.StartDate, e.EndDate,
		e.StartTime, e.EndTime, e.Source, e.ExternalProvider, e.ExternalID, e.ExternalCalendarID,
		e.ExternalHref, e.ETag, e.ReadOnly, e.SyncError, e.SyncFailedAt}
	if stampSynced {
		syncedAt = "$19"
		args = append(args, e.ExternalSyncedAt)
	}
	updatedAt := "now()"
	if stampSynced {
		updatedAt = syncedAt
	}
	_, err := tx.Exec(ctx, `
		UPDATE calendar_events
		SET title = $2, description = $3, location = $4, all_day = $5, start_date = $6,
		    end_date = $7, start_time = $8::time, end_time = $9::time, source = $10,
		    external_provider = $11, external_id = $12, external_calendar_id = $13,
		    external_href = $14, etag = $15, read_only = $16, sync_error = $17, sync_failed_at = $18,
		    external_synced_at = `+syncedAt+`,
		    updated_at = `+updatedAt+`
		WHERE id = $1 AND deleted_at IS NULL
	`, args...)
	if err != nil {
		return fmt.Errorf("updating calendar_event: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(row rowScanner) (calendar.Event, error) {
	var e calendar.Event
	var startTime, endTime *string
	if err := row.Scan(
		&e.ID, &e.Title, &e.Description, &e.Location, &e.AllDay, &e.StartDate, &e.EndDate,
		&startTime, &endTime, &e.Source, &e.ReadOnly,
		&e.ExternalProvider, &e.ExternalID, &e.ExternalCalendarID, &e.ExternalHref, &e.ETag,
		&e.ExternalSyncedAt, &e.SyncError, &e.SyncFailedAt, &e.CreatedAt, &e.UpdatedAt, &e.DeletedAt,
	); err != nil {
		return calendar.Event{}, err
	}
	e.StartTime = normalizeTime(startTime)
	e.EndTime = normalizeTime(endTime)
	return e, nil
}

// normalizeTime turns Postgres' "HH:MM:SS" into the "HH:MM" the domain and API use. An
// unparseable value is passed through rather than dropped, so a surprise never silently hides a
// time.
func normalizeTime(raw *string) *string {
	if raw == nil {
		return nil
	}
	parsed, err := time.Parse("15:04:05", *raw)
	if err != nil {
		return raw
	}
	out := parsed.Format("15:04")
	return &out
}
