// Package postgres implements lifemapprefs.Repository — the only file in the lifemapprefs module
// that imports a SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/lifemapprefs"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ lifemapprefs.Repository = (*Repository)(nil)

func (r *Repository) Get(ctx context.Context, appUserID uuid.UUID) (lifemapprefs.Preferences, error) {
	p := lifemapprefs.Preferences{AppUserID: appUserID}
	var raw []byte
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT hidden_areas, updated_at FROM life_map_preferences WHERE app_user_id = $1
		`, appUserID).Scan(&raw, &p.UpdatedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Never saved: not an error. Return an empty preference set so the handler answers 200
			// with `hiddenAreas: []` instead of 404 — the frontend treats "never saved" as "nothing
			// hidden" (see internal/lifemapprefs/domain.go).
			p.HiddenAreas = []string{}
			return p, nil
		}
		return lifemapprefs.Preferences{}, fmt.Errorf("querying life_map_preferences: %w", err)
	}
	if err := json.Unmarshal(raw, &p.HiddenAreas); err != nil {
		return lifemapprefs.Preferences{}, fmt.Errorf("decoding hidden_areas: %w", err)
	}
	if p.HiddenAreas == nil {
		// Slices returned in JSON must never reach the caller as nil — see docs/code-quality.md —
		// a nil slice serializes as `null`, breaking any .map() on the frontend.
		p.HiddenAreas = []string{}
	}
	return p, nil
}

func (r *Repository) Set(ctx context.Context, appUserID uuid.UUID, hiddenAreas []string) (lifemapprefs.Preferences, error) {
	if hiddenAreas == nil {
		hiddenAreas = []string{}
	}
	items, err := json.Marshal(hiddenAreas)
	if err != nil {
		return lifemapprefs.Preferences{}, fmt.Errorf("encoding hidden_areas: %w", err)
	}

	p := lifemapprefs.Preferences{AppUserID: appUserID, HiddenAreas: hiddenAreas}
	err = db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO life_map_preferences (app_user_id, hidden_areas, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (app_user_id) DO UPDATE SET hidden_areas = EXCLUDED.hidden_areas, updated_at = now()
			RETURNING updated_at
		`, appUserID, items).Scan(&p.UpdatedAt)
	})
	if err != nil {
		return lifemapprefs.Preferences{}, fmt.Errorf("upserting life_map_preferences: %w", err)
	}
	return p, nil
}
