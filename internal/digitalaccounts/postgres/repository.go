// Package postgres implements digitalaccounts.Repository — the only file in the digitalaccounts
// module that imports a SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/digitalaccounts"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ digitalaccounts.Repository = (*Repository)(nil)

func (r *Repository) List(ctx context.Context, personID *uuid.UUID, category *digitalaccounts.Category, status *digitalaccounts.Status, email *string) ([]digitalaccounts.Account, error) {
	var result []digitalaccounts.Account
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, person_id, service_name, category, email_used, mfa_method, recovery_notes,
			       status, last_reviewed_at, created_at
			FROM digital_accounts
			WHERE ($1::uuid IS NULL OR person_id = $1)
			  AND ($2::varchar IS NULL OR category = $2)
			  AND ($3::varchar IS NULL OR status = $3)
			  AND ($4::varchar IS NULL OR email_used = $4)
			ORDER BY service_name
		`, personID, category, status, email)
		if err != nil {
			return fmt.Errorf("querying digital accounts: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			a, err := scanAccount(rows)
			if err != nil {
				return err
			}
			result = append(result, a)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*digitalaccounts.Account, error) {
	var result *digitalaccounts.Account
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, person_id, service_name, category, email_used, mfa_method, recovery_notes,
			       status, last_reviewed_at, created_at
			FROM digital_accounts
			WHERE id = $1
		`, id)
		a, err := scanAccount(row)
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

func (r *Repository) Create(ctx context.Context, a digitalaccounts.Account) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO digital_accounts (id, person_id, service_name, category, email_used,
			                               mfa_method, recovery_notes, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, a.ID, a.PersonID, a.ServiceName, a.Category, a.EmailUsed, a.MFAMethod, a.RecoveryNotes,
			a.Status)
		return err
	})
	if err != nil {
		return translateWriteError(err, "inserting digital account")
	}
	return nil
}

func (r *Repository) Update(ctx context.Context, a digitalaccounts.Account) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE digital_accounts
			SET person_id = $2, service_name = $3, category = $4, email_used = $5, mfa_method = $6,
			    recovery_notes = $7, status = $8
			WHERE id = $1
		`, a.ID, a.PersonID, a.ServiceName, a.Category, a.EmailUsed, a.MFAMethod, a.RecoveryNotes,
			a.Status)
		return err
	})
	if err != nil {
		return translateWriteError(err, "updating digital account")
	}
	return nil
}

func (r *Repository) MarkReviewed(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE digital_accounts SET last_reviewed_at = now() WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("marking digital account reviewed: %w", err)
		}
		return nil
	})
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM digital_accounts WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting digital account: %w", err)
		}
		return nil
	})
}

// translateWriteError turns a foreign-key violation into an apperr.Validation instead of a raw
// 500 — same pattern as internal/assets/postgres/repository.go.
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

func scanAccount(row rowScanner) (digitalaccounts.Account, error) {
	var a digitalaccounts.Account
	err := row.Scan(&a.ID, &a.PersonID, &a.ServiceName, &a.Category, &a.EmailUsed, &a.MFAMethod,
		&a.RecoveryNotes, &a.Status, &a.LastReviewedAt, &a.CreatedAt)
	return a, err
}
