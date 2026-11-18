// Package postgres implements auditevents.Repository — the only file in the module that imports a
// SQL driver. audit_events is control-plane (public schema, cross-tenant), so every query goes
// through db.WithControlTx.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/auditevents"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ auditevents.Repository = (*Repository)(nil)

const selectColumns = `
	id, event_type, severity, user_id, user_email, tenant_id, ip, user_agent,
	method, path, status_code, request_id, message, detail, metadata, duration_ms, created_at`

func (r *Repository) Insert(ctx context.Context, ev auditevents.Event) error {
	meta, err := json.Marshal(ev.Metadata)
	if err != nil {
		return fmt.Errorf("marshaling audit event metadata: %w", err)
	}
	if ev.Metadata == nil {
		meta = []byte("{}")
	}
	if ev.ID == uuid.Nil {
		ev.ID = uuid.New()
	}
	return db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO audit_events (
				id, event_type, severity, user_id, user_email, tenant_id, ip, user_agent,
				method, path, status_code, request_id, message, detail, metadata, duration_ms, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
			ev.ID, ev.Type, ev.Severity, ev.UserID, nullable(ev.UserEmail), ev.TenantID, nullable(ev.IP),
			nullable(ev.UserAgent), nullable(ev.Method), nullable(ev.Path), ev.StatusCode, nullable(ev.RequestID),
			nullable(ev.Message), nullable(ev.Detail), meta, ev.DurationMS, ev.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("inserting audit event: %w", err)
		}
		return nil
	})
}

func (r *Repository) List(ctx context.Context, filter auditevents.Filter) ([]auditevents.Event, error) {
	var result []auditevents.Event
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		where, args := buildWhere(filter)
		args = append(args, filter.Limit, filter.Offset)
		query := `SELECT` + selectColumns + ` FROM audit_events` + where +
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("querying audit_events: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			ev, err := scanEvent(rows)
			if err != nil {
				return err
			}
			result = append(result, ev)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) Count(ctx context.Context, filter auditevents.Filter) (int, error) {
	var total int
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		where, args := buildWhere(filter)
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_events`+where, args...).Scan(&total)
	})
	return total, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*auditevents.Event, error) {
	var result *auditevents.Event
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT`+selectColumns+` FROM audit_events WHERE id = $1`, id)
		ev, err := scanEvent(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &ev
		return nil
	})
	return result, err
}

func (r *Repository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	var deleted int64
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM audit_events WHERE created_at < $1`, cutoff)
		if err != nil {
			return fmt.Errorf("deleting expired audit events: %w", err)
		}
		deleted = tag.RowsAffected()
		return nil
	})
	return deleted, err
}

func (r *Repository) DeleteAll(ctx context.Context) (int64, error) {
	var deleted int64
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM audit_events`)
		if err != nil {
			return fmt.Errorf("clearing audit events: %w", err)
		}
		deleted = tag.RowsAffected()
		return nil
	})
	return deleted, err
}

func (r *Repository) Stats(ctx context.Context, since time.Time) (auditevents.Stats, error) {
	var stats auditevents.Stats
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE severity IN ('error','critical')),
			       count(*) FILTER (WHERE severity = 'warning'),
			       count(*) FILTER (WHERE event_type = 'login_success'),
			       count(*) FILTER (WHERE event_type = 'login_failure')
			FROM audit_events WHERE created_at >= $1`, since,
		).Scan(&stats.Total, &stats.Errors, &stats.Warnings, &stats.Logins, &stats.FailedLogins); err != nil {
			return fmt.Errorf("counting audit events: %w", err)
		}

		dayRows, err := tx.Query(ctx, `
			SELECT to_char((created_at AT TIME ZONE 'America/Sao_Paulo')::date, 'YYYY-MM-DD') AS day,
			       count(*),
			       count(*) FILTER (WHERE severity IN ('error','critical'))
			FROM audit_events WHERE created_at >= $1
			GROUP BY day ORDER BY day`, since)
		if err != nil {
			return fmt.Errorf("grouping audit events by day: %w", err)
		}
		defer dayRows.Close()
		for dayRows.Next() {
			var d auditevents.DayCount
			if err := dayRows.Scan(&d.Day, &d.Total, &d.Errors); err != nil {
				return err
			}
			stats.ByDay = append(stats.ByDay, d)
		}
		if err := dayRows.Err(); err != nil {
			return err
		}

		typeRows, err := tx.Query(ctx, `
			SELECT event_type, count(*) FROM audit_events WHERE created_at >= $1
			GROUP BY event_type ORDER BY count(*) DESC`, since)
		if err != nil {
			return fmt.Errorf("grouping audit events by type: %w", err)
		}
		defer typeRows.Close()
		for typeRows.Next() {
			var t auditevents.TypeCount
			if err := typeRows.Scan(&t.Type, &t.Count); err != nil {
				return err
			}
			stats.ByType = append(stats.ByType, t)
		}
		if err := typeRows.Err(); err != nil {
			return err
		}

		loginRows, err := tx.Query(ctx, `SELECT`+selectColumns+` FROM audit_events
			WHERE event_type IN ('login_success','login_failure')
			ORDER BY created_at DESC LIMIT 20`)
		if err != nil {
			return fmt.Errorf("loading recent logins: %w", err)
		}
		defer loginRows.Close()
		for loginRows.Next() {
			ev, err := scanEvent(loginRows)
			if err != nil {
				return err
			}
			stats.RecentLogins = append(stats.RecentLogins, ev)
		}
		return loginRows.Err()
	})
	return stats, err
}

// buildWhere renders the filter predicates and their bind args, numbered from $1. The caller
// appends LIMIT/OFFSET and must account for the returned args length.
func buildWhere(filter auditevents.Filter) (string, []any) {
	var clauses []string
	var args []any
	add := func(clause string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf(clause, len(args)))
	}
	if filter.Type != "" {
		add("event_type = $%d", filter.Type)
	}
	if filter.Severity != "" {
		add("severity = $%d", filter.Severity)
	}
	if filter.Search != "" {
		args = append(args, "%"+filter.Search+"%")
		n := len(args)
		clauses = append(clauses, fmt.Sprintf(
			"(message ILIKE $%d OR detail ILIKE $%d OR path ILIKE $%d OR user_email ILIKE $%d)", n, n, n, n))
	}
	if filter.From != nil {
		add("created_at >= $%d", *filter.From)
	}
	if filter.To != nil {
		add("created_at <= $%d", *filter.To)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(row rowScanner) (auditevents.Event, error) {
	var ev auditevents.Event
	var meta []byte
	var userEmail, ip, userAgent, method, path, requestID, message, detail *string
	if err := row.Scan(
		&ev.ID, &ev.Type, &ev.Severity, &ev.UserID, &userEmail, &ev.TenantID, &ip, &userAgent,
		&method, &path, &ev.StatusCode, &requestID, &message, &detail, &meta, &ev.DurationMS, &ev.CreatedAt,
	); err != nil {
		return ev, err
	}
	ev.UserEmail = deref(userEmail)
	ev.IP = deref(ip)
	ev.UserAgent = deref(userAgent)
	ev.Method = deref(method)
	ev.Path = deref(path)
	ev.RequestID = deref(requestID)
	ev.Message = deref(message)
	ev.Detail = deref(detail)
	if len(meta) > 0 {
		_ = json.Unmarshal(meta, &ev.Metadata)
	}
	if ev.Metadata == nil {
		ev.Metadata = map[string]any{}
	}
	return ev, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
