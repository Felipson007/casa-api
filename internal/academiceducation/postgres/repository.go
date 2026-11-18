// Package postgres implements academiceducation.Repository — mirrors
// internal/jobexperience/postgres's shape exactly for the sibling "Educação" table.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/academiceducation"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ academiceducation.Repository = (*Repository)(nil)

const selectEducation = `
	SELECT id, person_id, institution, course, education_level, start_date, end_date,
	       notes, diploma_document_id, created_at, updated_at
	FROM academic_educations`

func (r *Repository) List(ctx context.Context, personID uuid.UUID) ([]academiceducation.Education, error) {
	result := []academiceducation.Education{}
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, selectEducation+` WHERE person_id = $1 ORDER BY start_date DESC`, personID)
		if err != nil {
			return fmt.Errorf("querying academic_educations: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEducation(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*academiceducation.Education, error) {
	var result *academiceducation.Education
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		e, err := scanEducation(tx.QueryRow(ctx, selectEducation+` WHERE id = $1`, id))
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

func (r *Repository) Create(ctx context.Context, e academiceducation.Education) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO academic_educations
			  (id, person_id, institution, course, education_level, start_date, end_date, notes,
			   diploma_document_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, e.ID, e.PersonID, e.Institution, e.Course, e.EducationLevel, e.StartDate, e.EndDate,
			e.Notes, e.DiplomaDocumentID)
		return err
	})
	if err != nil {
		return translateWriteError(err, "inserting academic education")
	}
	return nil
}

func (r *Repository) Update(ctx context.Context, e academiceducation.Education) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE academic_educations
			SET institution = $2, course = $3, education_level = $4, start_date = $5, end_date = $6,
			    notes = $7, diploma_document_id = $8, updated_at = now()
			WHERE id = $1
		`, e.ID, e.Institution, e.Course, e.EducationLevel, e.StartDate, e.EndDate, e.Notes,
			e.DiplomaDocumentID)
		return err
	})
	if err != nil {
		return translateWriteError(err, "updating academic education")
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM academic_educations WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting academic education: %w", err)
		}
		return nil
	})
}

// translateWriteError turns a check-constraint violation (end_date before start_date) or a
// foreign-key violation into an apperr.Validation instead of a raw 500 — mirrors
// internal/jobexperience/postgres's own.
func translateWriteError(err error, action string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503": // foreign_key_violation
			return apperr.Validation("personId or diplomaDocumentId does not reference an existing row")
		case "23514": // check_violation
			return apperr.Validation("endDate cannot be before startDate", "endDate")
		}
	}
	return fmt.Errorf("%s: %w", action, err)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEducation(row rowScanner) (academiceducation.Education, error) {
	var e academiceducation.Education
	err := row.Scan(&e.ID, &e.PersonID, &e.Institution, &e.Course, &e.EducationLevel,
		&e.StartDate, &e.EndDate, &e.Notes, &e.DiplomaDocumentID, &e.CreatedAt, &e.UpdatedAt)
	return e, err
}
