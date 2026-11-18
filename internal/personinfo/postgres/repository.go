// Package postgres implements personinfo.Repository — the only file in this module that imports a
// SQL driver. History writes go directly to the generic audit_logs table (table_name
// "person_information"), inside the same transaction as the update they snapshot — see
// personinfo.Repository's doc comment on why this bypasses internal/audit's own Service/HTTP
// surface entirely rather than calling into it.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/personinfo"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
)

const auditTableName = "person_information"

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ personinfo.Repository = (*Repository)(nil)

const informationColumns = `id, person_id, category, key, value, document_id, origin, document_type, created_at`

func scanInformation(row rowScanner) (personinfo.Information, error) {
	var i personinfo.Information
	err := row.Scan(&i.ID, &i.PersonID, &i.Category, &i.Key, &i.Value, &i.DocumentID, &i.Origin, &i.DocumentType, &i.CreatedAt)
	return i, err
}

func (r *Repository) List(ctx context.Context, personID uuid.UUID) ([]personinfo.Information, error) {
	var result []personinfo.Information
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT `+informationColumns+`
			FROM person_information
			WHERE person_id = $1
			ORDER BY category, key
		`, personID)
		if err != nil {
			return fmt.Errorf("querying person_information: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			i, err := scanInformation(rows)
			if err != nil {
				return fmt.Errorf("scanning person_information row: %w", err)
			}
			result = append(result, i)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*personinfo.Information, error) {
	var result *personinfo.Information
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+informationColumns+` FROM person_information WHERE id = $1`, id)
		i, err := scanInformation(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("scanning person_information row: %w", err)
		}
		result = &i
		return nil
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, i personinfo.Information) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return insertInformation(ctx, tx, i)
	})
	if err != nil {
		return translateWriteError(err, "inserting person_information")
	}
	return nil
}

// CreateBatch inserts every item in one transaction — mirrors Java's criarEmLote (saveAll).
func (r *Repository) CreateBatch(ctx context.Context, items []personinfo.Information) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		for _, i := range items {
			if err := insertInformation(ctx, tx, i); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return translateWriteError(err, "inserting person_information batch")
	}
	return nil
}

func insertInformation(ctx context.Context, tx pgx.Tx, i personinfo.Information) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO person_information (id, person_id, category, key, value, document_id, origin, document_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, i.ID, i.PersonID, i.Category, i.Key, i.Value, i.DocumentID, i.Origin, i.DocumentType)
	return err
}

// Update persists i. When snapshot is non-nil, it's inserted into audit_logs in the SAME
// transaction as the update — mirrors finance.Repository.Update's atomicity.
func (r *Repository) Update(ctx context.Context, i personinfo.Information, snapshot *personinfo.HistoryEntry) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if snapshot != nil {
			if err := insertHistorySnapshot(ctx, tx, i.ID, *snapshot); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `
			UPDATE person_information SET category = $2, key = $3, value = $4, document_type = $5
			WHERE id = $1
		`, i.ID, i.Category, i.Key, i.Value, i.DocumentType)
		return err
	})
	if err != nil {
		return translateWriteError(err, "updating person_information")
	}
	return nil
}

// Delete removes an information fact outright. Its audit_logs rows are NOT cascade-deleted (no FK
// from audit_logs back to person_information — it's a generic table keyed loosely by table_name+
// row_id, on purpose, so it can log ANY table) — history for a deleted fact is retained
// indefinitely as an orphaned audit trail, same as every other *_historico-turned-audit_logs table.
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM person_information WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting person_information: %w", err)
		}
		return nil
	})
}

func (r *Repository) ListHistory(ctx context.Context, informationID uuid.UUID) ([]personinfo.HistoryEntry, error) {
	var result []personinfo.HistoryEntry
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, old_data, changed_at
			FROM audit_logs
			WHERE table_name = $1 AND row_id = $2
			ORDER BY changed_at DESC
		`, auditTableName, informationID)
		if err != nil {
			return fmt.Errorf("querying audit_logs: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var id uuid.UUID
			var oldData []byte
			var changedAt time.Time
			if err := rows.Scan(&id, &oldData, &changedAt); err != nil {
				return fmt.Errorf("scanning audit_logs row: %w", err)
			}
			h, err := decodeHistoryEntry(id, oldData, changedAt)
			if err != nil {
				return err
			}
			result = append(result, h)
		}
		return rows.Err()
	})
	return result, err
}

// Equalize applies every planned update in one transaction — mirrors Java's equalizar being a
// single @Transactional method covering the whole batch, not N separate round trips.
func (r *Repository) Equalize(ctx context.Context, updates []personinfo.EqualizeUpdate) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		for _, u := range updates {
			if u.Snapshot != nil {
				if err := insertHistorySnapshot(ctx, tx, u.ID, *u.Snapshot); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE person_information SET value = $2 WHERE id = $1`, u.ID, u.NewValue); err != nil {
				return fmt.Errorf("updating person_information value: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("equalizing person_information: %w", err)
	}
	return nil
}

type historySnapshot struct {
	Category     personinfo.Category `json:"category"`
	Key          string              `json:"key"`
	Value        string              `json:"value"`
	DocumentType *string             `json:"documentType,omitempty"`
}

func insertHistorySnapshot(ctx context.Context, tx pgx.Tx, rowID uuid.UUID, h personinfo.HistoryEntry) error {
	data, err := json.Marshal(historySnapshot{Category: h.Category, Key: h.Key, Value: h.Value, DocumentType: h.DocumentType})
	if err != nil {
		return fmt.Errorf("marshaling person_information history snapshot: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (id, table_name, row_id, old_data)
		VALUES ($1, $2, $3, $4)
	`, h.ID, auditTableName, rowID, data); err != nil {
		return fmt.Errorf("inserting audit_logs snapshot: %w", err)
	}
	return nil
}

func decodeHistoryEntry(id uuid.UUID, oldData []byte, changedAt time.Time) (personinfo.HistoryEntry, error) {
	var snap historySnapshot
	if err := json.Unmarshal(oldData, &snap); err != nil {
		return personinfo.HistoryEntry{}, fmt.Errorf("unmarshaling audit_logs old_data: %w", err)
	}
	return personinfo.HistoryEntry{
		ID: id, Category: snap.Category, Key: snap.Key, Value: snap.Value, DocumentType: snap.DocumentType, ChangedAt: changedAt,
	}, nil
}

// translateWriteError turns a foreign-key violation into an apperr.Validation instead of a raw
// 500 — same pattern as internal/finance/postgres/repository.go.
func translateWriteError(err error, action string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
		return apperr.Validation("personId/documentId does not reference an existing row")
	}
	return fmt.Errorf("%s: %w", action, err)
}

// rowScanner is satisfied by both pgx.Rows and pgx.Row.
type rowScanner interface {
	Scan(dest ...any) error
}
