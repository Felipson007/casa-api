// Package postgres implements finance.Repository — the only file in the finance module that
// imports a SQL driver. Every query goes through db.WithTx. current_value/balance are read/written
// as string via ::text/::numeric casts, same reasoning as loans/expenses/income/assets.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/finance"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ finance.Repository = (*Repository)(nil)

func (r *Repository) List(ctx context.Context) ([]finance.Balance, error) {
	var result []finance.Balance
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, person_id, name, balance_type, current_value::text, updated_on, notes, created_at, bank_account_id
			FROM financial_balances
			ORDER BY name
		`)
		if err != nil {
			return fmt.Errorf("querying financial_balances: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			b, err := scanBalance(rows)
			if err != nil {
				return err
			}
			result = append(result, b)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*finance.Balance, error) {
	var result *finance.Balance
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, person_id, name, balance_type, current_value::text, updated_on, notes, created_at, bank_account_id
			FROM financial_balances
			WHERE id = $1
		`, id)
		b, err := scanBalance(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &b
		return nil
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, b finance.Balance) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO financial_balances (id, person_id, name, balance_type, current_value, updated_on, notes)
			VALUES ($1, $2, $3, $4, $5::numeric, $6, $7)
		`, b.ID, b.PersonID, b.Name, b.Type, b.CurrentValue, b.UpdatedOn, b.Notes)
		return err
	})
	if err != nil {
		return translateWriteError(err, "inserting financial balance")
	}
	return nil
}

// Update persists b. When snapshot is non-nil, it's inserted in the SAME transaction as the
// update — see finance.Repository's doc comment for why this can't be two separate round trips.
func (r *Repository) Update(ctx context.Context, b finance.Balance, snapshot *finance.BalanceHistoryEntry) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if snapshot != nil {
			if _, err := tx.Exec(ctx, `
				INSERT INTO financial_balance_history (id, balance_id, balance, recorded_on)
				VALUES ($1, $2, $3::numeric, $4)
			`, snapshot.ID, snapshot.BalanceID, snapshot.Balance, snapshot.RecordedOn); err != nil {
				return fmt.Errorf("inserting financial_balance_history snapshot: %w", err)
			}
		}
		_, err := tx.Exec(ctx, `
			UPDATE financial_balances
			SET person_id = $2, name = $3, balance_type = $4, current_value = $5::numeric,
			    updated_on = $6, notes = $7
			WHERE id = $1
		`, b.ID, b.PersonID, b.Name, b.Type, b.CurrentValue, b.UpdatedOn, b.Notes)
		return err
	})
	if err != nil {
		return translateWriteError(err, "updating financial balance")
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM financial_balances WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting financial balance: %w", err)
		}
		return nil
	})
}

func (r *Repository) ListHistory(ctx context.Context, balanceID uuid.UUID) ([]finance.BalanceHistoryEntry, error) {
	var result []finance.BalanceHistoryEntry
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, balance_id, balance::text, recorded_on, created_at
			FROM financial_balance_history
			WHERE balance_id = $1
			ORDER BY created_at DESC
		`, balanceID)
		if err != nil {
			return fmt.Errorf("querying financial_balance_history: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var h finance.BalanceHistoryEntry
			if err := rows.Scan(&h.ID, &h.BalanceID, &h.Balance, &h.RecordedOn, &h.CreatedAt); err != nil {
				return fmt.Errorf("scanning financial_balance_history row: %w", err)
			}
			result = append(result, h)
		}
		return rows.Err()
	})
	return result, err
}

// translateWriteError turns a foreign-key violation into an apperr.Validation instead of a raw
// 500 — same pattern as internal/expenses/postgres/repository.go.
func translateWriteError(err error, action string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
		return apperr.Validation("personId does not reference an existing row")
	}
	return fmt.Errorf("%s: %w", action, err)
}

// rowScanner is satisfied by both pgx.Rows and pgx.Row.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanBalance(row rowScanner) (finance.Balance, error) {
	var b finance.Balance
	err := row.Scan(&b.ID, &b.PersonID, &b.Name, &b.Type, &b.CurrentValue, &b.UpdatedOn, &b.Notes, &b.CreatedAt, &b.BankAccountID)
	return b, err
}
