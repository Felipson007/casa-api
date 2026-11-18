// Package postgres implements documents.Repository — the only file in the documents module that
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

	"github.com/atlas/atlas-go/internal/documents"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ documents.Repository = (*Repository)(nil)

func (r *Repository) List(ctx context.Context, personID, assetID, petID *uuid.UUID, query string) ([]documents.Document, error) {
	var result []documents.Document
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, person_id, original_filename, file_path, file_type, origin, document_date,
			       tags, asset_id, pet_id, description, institution, amount::text, expiration_date,
			       external_url, notes, created_at, statement_total::text, statement_closing_date,
			       extraction_status, extraction_delta::text, extraction_note, content_hash,
			       tax_doc_kind, pending_extraction::text
			FROM documents
			WHERE ($1::uuid IS NULL OR person_id = $1)
			  AND ($2::uuid IS NULL OR asset_id = $2)
			  AND ($3::uuid IS NULL OR pet_id = $3)
			  AND ($4 = '' OR original_filename ILIKE '%' || $4 || '%'
			       OR COALESCE(description, '') ILIKE '%' || $4 || '%'
			       OR COALESCE(institution, '') ILIKE '%' || $4 || '%'
			       OR COALESCE(tags, '') ILIKE '%' || $4 || '%')
			ORDER BY created_at DESC
		`, personID, assetID, petID, query)
		if err != nil {
			return fmt.Errorf("querying documents: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			d, err := scanDocument(rows)
			if err != nil {
				return err
			}
			result = append(result, d)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*documents.Document, error) {
	var result *documents.Document
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, person_id, original_filename, file_path, file_type, origin, document_date,
			       tags, asset_id, pet_id, description, institution, amount::text, expiration_date,
			       external_url, notes, created_at, statement_total::text, statement_closing_date,
			       extraction_status, extraction_delta::text, extraction_note, content_hash,
			       tax_doc_kind, pending_extraction::text
			FROM documents
			WHERE id = $1
		`, id)
		d, err := scanDocument(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &d
		return nil
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, d documents.Document) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO documents (id, person_id, original_filename, file_path, file_type, origin,
			                        document_date, tags, asset_id, pet_id, description, institution,
			                        amount, expiration_date, external_url, notes, content_hash,
			                        tax_doc_kind)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::numeric, $14, $15, $16, $17, $18)
		`, d.ID, d.PersonID, d.OriginalFilename, d.FilePath, d.FileType, d.Origin, d.DocumentDate,
			d.Tags, d.AssetID, d.PetID, d.Description, d.Institution, d.Amount, d.ExpirationDate,
			d.ExternalURL, d.Notes, d.ContentHash, d.TaxDocKind)
		return err
	})
	if err != nil {
		return translateWriteError(err, "inserting document")
	}
	return nil
}

func (r *Repository) Update(ctx context.Context, d documents.Document) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE documents
			SET person_id = $2, document_date = $3, tags = $4, asset_id = $5, pet_id = $6,
			    description = $7, institution = $8, amount = $9::numeric, expiration_date = $10,
			    external_url = $11, notes = $12, origin = $13, tax_doc_kind = $14
			WHERE id = $1
		`, d.ID, d.PersonID, d.DocumentDate, d.Tags, d.AssetID, d.PetID, d.Description, d.Institution,
			d.Amount, d.ExpirationDate, d.ExternalURL, d.Notes, d.Origin, d.TaxDocKind)
		return err
	})
	if err != nil {
		return translateWriteError(err, "updating document")
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM documents WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting document: %w", err)
		}
		return nil
	})
}

// UpdateFilePath rewrites only a document's file_path — used by the one-time local→Drive migration
// so it never clobbers metadata the generic Update would carry.
func (r *Repository) UpdateFilePath(ctx context.Context, id uuid.UUID, filePath string) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE documents SET file_path = $2 WHERE id = $1`, id, filePath); err != nil {
			return fmt.Errorf("updating document file path: %w", err)
		}
		return nil
	})
}

// translateWriteError turns a foreign-key violation into an apperr.Validation instead of a raw
// 500 — same pattern as internal/expenses/postgres/repository.go.
func translateWriteError(err error, action string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
		return apperr.Validation("personId, assetId, or petId does not reference an existing row")
	}
	return fmt.Errorf("%s: %w", action, err)
}

// rowScanner is satisfied by both pgx.Rows and pgx.Row.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanDocument(row rowScanner) (documents.Document, error) {
	var d documents.Document
	err := row.Scan(&d.ID, &d.PersonID, &d.OriginalFilename, &d.FilePath, &d.FileType, &d.Origin,
		&d.DocumentDate, &d.Tags, &d.AssetID, &d.PetID, &d.Description, &d.Institution, &d.Amount,
		&d.ExpirationDate, &d.ExternalURL, &d.Notes, &d.CreatedAt, &d.StatementTotal,
		&d.StatementClosingDate, &d.ExtractionStatus, &d.ExtractionDelta, &d.ExtractionNote,
		&d.ContentHash, &d.TaxDocKind, &d.PendingExtraction)
	return d, err
}

// FindByContentHash returns the document whose stored bytes hash to hash, or nil — how a re-upload
// of a file already in the system is recognised (see documents.Service.Upload).
func (r *Repository) FindByContentHash(ctx context.Context, hash string) (*documents.Document, error) {
	var result *documents.Document
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, person_id, original_filename, file_path, file_type, origin, document_date,
			       tags, asset_id, pet_id, description, institution, amount::text, expiration_date,
			       external_url, notes, created_at, statement_total::text, statement_closing_date,
			       extraction_status, extraction_delta::text, extraction_note, content_hash,
			       tax_doc_kind, pending_extraction::text
			FROM documents
			WHERE content_hash = $1
			ORDER BY created_at
			LIMIT 1
		`, hash)
		d, err := scanDocument(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &d
		return nil
	})
	return result, err
}

// UpdateExtraction persists only the itemized-extraction reconciliation columns (migration
// 000013), leaving every other document field untouched — the generic Update never writes these,
// so Service.Extract / Service.Reconcile go through here instead.
func (r *Repository) UpdateExtraction(ctx context.Context, id uuid.UUID, res documents.ExtractionResult) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE documents
			SET statement_total = $2::numeric, statement_closing_date = $3,
			    extraction_status = $4, extraction_delta = $5::numeric, extraction_note = $6,
			    pending_extraction = CASE
			        WHEN $7::boolean THEN NULL
			        WHEN $8::jsonb IS NOT NULL THEN $8::jsonb
			        ELSE pending_extraction
			    END
			WHERE id = $1
		`, id, res.StatementTotal, res.StatementClosingDate, res.Status, res.Delta, res.Note,
			res.ClearPendingExtraction, res.PendingExtraction)
		if err != nil {
			return fmt.Errorf("updating document extraction state: %w", err)
		}
		return nil
	})
}
