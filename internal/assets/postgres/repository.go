// Package postgres implements assets.Repository — the only file in the assets module that
// imports a SQL driver. Every query goes through db.WithTx. Nullable money columns
// (acquisition_value, current_estimated_value, debt_amount) are read/written as *string via
// ::text casts, same reasoning as loans/expenses/income.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/assets"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ assets.Repository = (*Repository)(nil)

func (r *Repository) List(ctx context.Context, personID, categoryID *uuid.UUID, status *assets.Status) ([]assets.Asset, error) {
	var result []assets.Asset
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, person_id, name, category_id, identification, acquisition_value::text,
			       acquisition_date, current_estimated_value::text, status, has_debt,
			       debt_amount::text, notes, attributes, created_at
			FROM assets
			WHERE ($1::uuid IS NULL OR person_id = $1)
			  AND ($2::uuid IS NULL OR category_id = $2)
			  AND ($3::varchar IS NULL OR status = $3)
			ORDER BY name
		`, personID, categoryID, status)
		if err != nil {
			return fmt.Errorf("querying assets: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			a, err := scanAsset(rows)
			if err != nil {
				return err
			}
			result = append(result, a)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*assets.Asset, error) {
	var result *assets.Asset
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, person_id, name, category_id, identification, acquisition_value::text,
			       acquisition_date, current_estimated_value::text, status, has_debt,
			       debt_amount::text, notes, attributes, created_at
			FROM assets
			WHERE id = $1
		`, id)
		a, err := scanAsset(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &a
		return nil
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, a assets.Asset) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO assets (id, person_id, name, category_id, identification,
			                     acquisition_value, acquisition_date, current_estimated_value,
			                     status, has_debt, debt_amount, notes, attributes)
			VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8::numeric, $9, $10, $11::numeric, $12, $13)
		`, a.ID, a.PersonID, a.Name, a.CategoryID, a.Identification, a.AcquisitionValue,
			a.AcquisitionDate, a.CurrentEstimatedValue, a.Status, a.HasDebt, a.DebtAmount, a.Notes,
			a.Attributes)
		return err
	})
	if err != nil {
		return translateWriteError(err, "inserting asset")
	}
	return nil
}

func (r *Repository) Update(ctx context.Context, a assets.Asset) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE assets
			SET person_id = $2, name = $3, category_id = $4, identification = $5,
			    acquisition_value = $6::numeric, acquisition_date = $7,
			    current_estimated_value = $8::numeric, status = $9, has_debt = $10,
			    debt_amount = $11::numeric, notes = $12, attributes = $13
			WHERE id = $1
		`, a.ID, a.PersonID, a.Name, a.CategoryID, a.Identification, a.AcquisitionValue,
			a.AcquisitionDate, a.CurrentEstimatedValue, a.Status, a.HasDebt, a.DebtAmount, a.Notes,
			a.Attributes)
		return err
	})
	if err != nil {
		return translateWriteError(err, "updating asset")
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM assets WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting asset: %w", err)
		}
		return nil
	})
}

// translateWriteError turns a foreign-key violation into an apperr.Validation instead of a raw
// 500 — same pattern as internal/expenses/postgres/repository.go.
func translateWriteError(err error, action string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
		return apperr.Validation("personId or categoryId does not reference an existing row")
	}
	return fmt.Errorf("%s: %w", action, err)
}

// rowScanner is satisfied by both pgx.Rows and pgx.Row.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanAsset(row rowScanner) (assets.Asset, error) {
	var a assets.Asset
	err := row.Scan(&a.ID, &a.PersonID, &a.Name, &a.CategoryID, &a.Identification,
		&a.AcquisitionValue, &a.AcquisitionDate, &a.CurrentEstimatedValue, &a.Status, &a.HasDebt,
		&a.DebtAmount, &a.Notes, &a.Attributes, &a.CreatedAt)
	return a, err
}
