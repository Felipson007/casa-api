// Package postgres implements jobexperience.Repository — the only file in the jobexperience module
// that imports a SQL driver. Every query goes through db.WithTx. Salary is read/written as string
// via ::text/::numeric casts, same convention as every other money field in this codebase.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/jobexperience"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ jobexperience.Repository = (*Repository)(nil)

const selectExperience = `
	SELECT id, person_id, company, title, position_level, salary::text, contract_type,
	       usd_amount::text, exchange_rate::text, start_date, end_date, notes, resume_document_id,
	       created_at, updated_at
	FROM job_experiences`

func (r *Repository) List(ctx context.Context, personID uuid.UUID) ([]jobexperience.Experience, error) {
	result := []jobexperience.Experience{}
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, selectExperience+` WHERE person_id = $1 ORDER BY start_date DESC`, personID)
		if err != nil {
			return fmt.Errorf("querying job_experiences: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanExperience(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*jobexperience.Experience, error) {
	var result *jobexperience.Experience
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		e, err := scanExperience(tx.QueryRow(ctx, selectExperience+` WHERE id = $1`, id))
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

func (r *Repository) Create(ctx context.Context, e jobexperience.Experience) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO job_experiences
			  (id, person_id, company, title, position_level, salary, contract_type, usd_amount,
			   exchange_rate, start_date, end_date, notes, resume_document_id)
			VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8::numeric, $9::numeric, $10, $11, $12, $13)
		`, e.ID, e.PersonID, e.Company, e.Title, e.PositionLevel, e.Salary, string(e.ContractType),
			e.USDAmount, e.ExchangeRate, e.StartDate, e.EndDate, e.Notes, e.ResumeDocumentID)
		return err
	})
	if err != nil {
		return translateWriteError(err, "inserting job experience")
	}
	return nil
}

func (r *Repository) Update(ctx context.Context, e jobexperience.Experience) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE job_experiences
			SET company = $2, title = $3, position_level = $4, salary = $5::numeric,
			    contract_type = $6, usd_amount = $7::numeric, exchange_rate = $8::numeric,
			    start_date = $9, end_date = $10, notes = $11, resume_document_id = $12, updated_at = now()
			WHERE id = $1
		`, e.ID, e.Company, e.Title, e.PositionLevel, e.Salary, string(e.ContractType), e.USDAmount,
			e.ExchangeRate, e.StartDate, e.EndDate, e.Notes, e.ResumeDocumentID)
		return err
	})
	if err != nil {
		return translateWriteError(err, "updating job experience")
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM job_experiences WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting job experience: %w", err)
		}
		return nil
	})
}

// translateWriteError turns a check-constraint violation (end_date before start_date) or a
// foreign-key violation into an apperr.Validation instead of a raw 500 — same pattern as
// internal/documents/postgres/repository.go. The service layer already validates dates before
// reaching here (belt-and-suspenders), so ck_job_experiences_dates should never actually fire.
func translateWriteError(err error, action string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503": // foreign_key_violation
			return apperr.Validation("personId or resumeDocumentId does not reference an existing row")
		case "23514": // check_violation
			return apperr.Validation("endDate cannot be before startDate", "endDate")
		}
	}
	return fmt.Errorf("%s: %w", action, err)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanExperience(row rowScanner) (jobexperience.Experience, error) {
	var e jobexperience.Experience
	var contractType string
	err := row.Scan(&e.ID, &e.PersonID, &e.Company, &e.Title, &e.PositionLevel, &e.Salary,
		&contractType, &e.USDAmount, &e.ExchangeRate, &e.StartDate, &e.EndDate, &e.Notes,
		&e.ResumeDocumentID, &e.CreatedAt, &e.UpdatedAt)
	e.ContractType = jobexperience.ContractType(contractType)
	return e, err
}
