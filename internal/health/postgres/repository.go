// Package postgres implements health.Repository — the only file in the health module that
// imports a SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ health.Repository = (*Repository)(nil)

// --- markers ---

func (r *Repository) ListMarkers(ctx context.Context) ([]health.MarkerCatalog, error) {
	var result []health.MarkerCatalog
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, name, unit, reference_min::text, reference_max::text, related_organ, created_at
			FROM health_marker_catalog
			ORDER BY name
		`)
		if err != nil {
			return fmt.Errorf("querying markers: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			m, err := scanMarker(rows)
			if err != nil {
				return err
			}
			result = append(result, m)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindMarkerByID(ctx context.Context, id uuid.UUID) (*health.MarkerCatalog, error) {
	var result *health.MarkerCatalog
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, name, unit, reference_min::text, reference_max::text, related_organ, created_at
			FROM health_marker_catalog
			WHERE id = $1
		`, id)
		m, err := scanMarker(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &m
		return nil
	})
	return result, err
}

func (r *Repository) CreateMarker(ctx context.Context, m health.MarkerCatalog) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO health_marker_catalog (id, name, unit, reference_min, reference_max, related_organ)
			VALUES ($1, $2, $3, $4::numeric, $5::numeric, $6)
		`, m.ID, m.Name, m.Unit, m.ReferenceMin, m.ReferenceMax, m.RelatedOrgan)
		if err != nil {
			return fmt.Errorf("inserting marker: %w", err)
		}
		return nil
	})
}

func (r *Repository) UpdateMarker(ctx context.Context, m health.MarkerCatalog) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE health_marker_catalog
			SET name = $2, unit = $3, reference_min = $4::numeric, reference_max = $5::numeric, related_organ = $6
			WHERE id = $1
		`, m.ID, m.Name, m.Unit, m.ReferenceMin, m.ReferenceMax, m.RelatedOrgan)
		if err != nil {
			return fmt.Errorf("updating marker: %w", err)
		}
		return nil
	})
}

func (r *Repository) DeleteMarker(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM health_marker_catalog WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting marker: %w", err)
		}
		return nil
	})
}

// --- records ---

func (r *Repository) ListRecords(ctx context.Context, personID, markerCatalogID *uuid.UUID) ([]health.Record, error) {
	var result []health.Record
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, person_id, type, record_date, status, amount::text, unit, marker_catalog_id,
			       organ, assessment_status, document_id, notes, created_at
			FROM health_records
			WHERE ($1::uuid IS NULL OR person_id = $1)
			  AND ($2::uuid IS NULL OR marker_catalog_id = $2)
			ORDER BY record_date DESC
		`, personID, markerCatalogID)
		if err != nil {
			return fmt.Errorf("querying health records: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			rec, err := scanRecord(rows)
			if err != nil {
				return err
			}
			result = append(result, rec)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindRecordByID(ctx context.Context, id uuid.UUID) (*health.Record, error) {
	var result *health.Record
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, person_id, type, record_date, status, amount::text, unit, marker_catalog_id,
			       organ, assessment_status, document_id, notes, created_at
			FROM health_records
			WHERE id = $1
		`, id)
		rec, err := scanRecord(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &rec
		return nil
	})
	return result, err
}

func (r *Repository) CreateRecord(ctx context.Context, rec health.Record) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO health_records (id, person_id, type, record_date, status, amount, unit,
			                             marker_catalog_id, organ, assessment_status, document_id, notes)
			VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8, $9, $10, $11, $12)
		`, rec.ID, rec.PersonID, rec.Type, rec.RecordDate, rec.Status, rec.Amount, rec.Unit,
			rec.MarkerCatalogID, rec.Organ, rec.AssessmentStatus, rec.DocumentID, rec.Notes)
		return err
	})
	if err != nil {
		return translateWriteError(err, "inserting health record")
	}
	return nil
}

func (r *Repository) UpdateRecord(ctx context.Context, rec health.Record) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE health_records
			SET type = $2, record_date = $3, status = $4, amount = $5::numeric, unit = $6,
			    marker_catalog_id = $7, organ = $8, assessment_status = $9, document_id = $10, notes = $11
			WHERE id = $1
		`, rec.ID, rec.Type, rec.RecordDate, rec.Status, rec.Amount, rec.Unit, rec.MarkerCatalogID,
			rec.Organ, rec.AssessmentStatus, rec.DocumentID, rec.Notes)
		return err
	})
	if err != nil {
		return translateWriteError(err, "updating health record")
	}
	return nil
}

func (r *Repository) DeleteRecord(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM health_records WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting health record: %w", err)
		}
		return nil
	})
}

// translateWriteError turns a foreign-key violation (person_id/marker_catalog_id/document_id
// referencing a nonexistent row) into an apperr.Validation instead of a raw 500 — same pattern as
// internal/expenses/postgres/repository.go.
func translateWriteError(err error, action string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
		return apperr.Validation("personId, markerCatalogId, or documentId does not reference an existing row")
	}
	return fmt.Errorf("%s: %w", action, err)
}

// rowScanner is satisfied by both pgx.Rows and pgx.Row.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanMarker(row rowScanner) (health.MarkerCatalog, error) {
	var m health.MarkerCatalog
	err := row.Scan(&m.ID, &m.Name, &m.Unit, &m.ReferenceMin, &m.ReferenceMax, &m.RelatedOrgan, &m.CreatedAt)
	return m, err
}

func scanRecord(row rowScanner) (health.Record, error) {
	var rec health.Record
	err := row.Scan(&rec.ID, &rec.PersonID, &rec.Type, &rec.RecordDate, &rec.Status, &rec.Amount,
		&rec.Unit, &rec.MarkerCatalogID, &rec.Organ, &rec.AssessmentStatus, &rec.DocumentID,
		&rec.Notes, &rec.CreatedAt)
	return rec, err
}
