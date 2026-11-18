package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/settings"
)

const singletonID = true

type postgresRepository struct {
	pool *pgxpool.Pool
}

// NewRepository builds a postgres-backed settings repository.
func NewRepository(pool *pgxpool.Pool) settings.Repository {
	return &postgresRepository{pool: pool}
}

// Compile-time check that this repository satisfies the interface.
var _ settings.Repository = (*postgresRepository)(nil)

const getSQL = `SELECT company_name, pix_key, logo_path, updated_at FROM app_settings WHERE id = $1`
const updateSQL = `UPDATE app_settings SET company_name = $1, pix_key = $2, logo_path = $3, updated_at = now() WHERE id = $4`

func (r *postgresRepository) Get(ctx context.Context) (*settings.Settings, error) {
	var s settings.Settings
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, getSQL, singletonID).Scan(&s.CompanyName, &s.PixKey, &s.LogoPath, &s.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, settings.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *postgresRepository) Update(ctx context.Context, s *settings.Settings) error {
	var affected int64
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, updateSQL, s.CompanyName, s.PixKey, s.LogoPath, singletonID)
		if err != nil {
			return err
		}
		affected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return settings.ErrNotFound
	}
	return nil
}
