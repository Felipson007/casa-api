// Package postgres implements audit.Repository — the only file in the audit module that imports a
// SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/audit"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ audit.Repository = (*Repository)(nil)

func (r *Repository) Create(ctx context.Context, a audit.AuditLog) error {
	oldJSON, err := json.Marshal(a.OldData)
	if err != nil {
		return fmt.Errorf("marshaling audit old_data: %w", err)
	}
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO audit_logs (id, table_name, row_id, old_data)
			VALUES ($1, $2, $3, $4)
		`, a.ID, a.TableName, a.RowID, oldJSON)
		if err != nil {
			return fmt.Errorf("inserting audit_log: %w", err)
		}
		return nil
	})
}

func (r *Repository) List(ctx context.Context, tableName string, rowID *uuid.UUID) ([]audit.AuditLog, error) {
	var result []audit.AuditLog
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, table_name, row_id, old_data, changed_at
			FROM audit_logs
			WHERE ($1::text = '' OR table_name = $1)
			  AND ($2::uuid IS NULL OR row_id = $2)
			ORDER BY changed_at DESC
		`, tableName, rowID)
		if err != nil {
			return fmt.Errorf("querying audit_logs: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			a, err := scanAuditLog(rows)
			if err != nil {
				return err
			}
			result = append(result, a)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*audit.AuditLog, error) {
	var result *audit.AuditLog
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, table_name, row_id, old_data, changed_at
			FROM audit_logs
			WHERE id = $1
		`, id)
		a, err := scanAuditLog(row)
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

// rowScanner is satisfied by both pgx.Rows and pgx.Row.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanAuditLog(row rowScanner) (audit.AuditLog, error) {
	var a audit.AuditLog
	var oldJSON []byte
	if err := row.Scan(&a.ID, &a.TableName, &a.RowID, &oldJSON, &a.ChangedAt); err != nil {
		return a, err
	}
	if len(oldJSON) > 0 {
		_ = json.Unmarshal(oldJSON, &a.OldData)
	}
	if a.OldData == nil {
		a.OldData = map[string]interface{}{}
	}
	return a, nil
}
