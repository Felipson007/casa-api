// Package postgres implements calendarsettings.Store over the calendar_sync_settings singleton.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/calendarsettings"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ calendarsettings.Store = (*Repository)(nil)

func (r *Repository) Get(ctx context.Context) (calendarsettings.Target, error) {
	var target calendarsettings.Target
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT target FROM calendar_sync_settings WHERE id = true`).Scan(&target)
	})
	if err != nil {
		return calendarsettings.TargetNone, fmt.Errorf("querying calendar_sync_settings: %w", err)
	}
	return target, nil
}

func (r *Repository) Set(ctx context.Context, t calendarsettings.Target) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO calendar_sync_settings (id, target, updated_at) VALUES (true, $1, now())
			ON CONFLICT (id) DO UPDATE SET target = EXCLUDED.target, updated_at = now()`, t)
		if err != nil {
			return fmt.Errorf("updating calendar_sync_settings: %w", err)
		}
		return nil
	})
}
