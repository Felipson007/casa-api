// Package postgres implements dashboardlayout.Repository — the only file in the dashboardlayout
// module that imports a SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/dashboardlayout"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ dashboardlayout.Repository = (*Repository)(nil)

func (r *Repository) Get(ctx context.Context, appUserID uuid.UUID) (dashboardlayout.Layout, error) {
	var l dashboardlayout.Layout
	l.AppUserID = appUserID
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT layout, updated_at FROM dashboard_layouts WHERE app_user_id = $1
		`, appUserID).Scan(&l.Items, &l.UpdatedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Never saved: not an error. Return a zero-value Layout (nil Items) so the handler
			// answers 200 with `layout: null` instead of 404 — the frontend treats null as "never
			// customized" and falls back to its built-in default (see internal/dashboardlayout/domain.go).
			return dashboardlayout.Layout{AppUserID: appUserID}, nil
		}
		return dashboardlayout.Layout{}, fmt.Errorf("querying dashboard_layouts: %w", err)
	}
	return l, nil
}

func (r *Repository) Set(ctx context.Context, appUserID uuid.UUID, items []byte) (dashboardlayout.Layout, error) {
	l := dashboardlayout.Layout{AppUserID: appUserID}
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO dashboard_layouts (app_user_id, layout, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (app_user_id) DO UPDATE SET layout = EXCLUDED.layout, updated_at = now()
			RETURNING layout, updated_at
		`, appUserID, items).Scan(&l.Items, &l.UpdatedAt)
	})
	if err != nil {
		return dashboardlayout.Layout{}, fmt.Errorf("upserting dashboard_layouts: %w", err)
	}
	return l, nil
}
