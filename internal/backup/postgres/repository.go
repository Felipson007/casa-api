package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/backup"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type postgresRepository struct {
	pool *pgxpool.Pool
}

// NewRepository builds a postgres-backed backup config repository.
func NewRepository(pool *pgxpool.Pool) backup.Repository {
	return &postgresRepository{pool: pool}
}

var _ backup.Repository = (*postgresRepository)(nil)

const (
	listSQL   = `SELECT id, hour, minute, last_automatic_backup_at, created_at FROM backup_configuration ORDER BY created_at`
	getSQL    = `SELECT id, hour, minute, last_automatic_backup_at, created_at FROM backup_configuration WHERE id = $1`
	createSQL = `INSERT INTO backup_configuration (id, hour, minute) VALUES ($1, $2, $3) RETURNING created_at`
	updateSQL = `UPDATE backup_configuration SET hour = $1, minute = $2 WHERE id = $3`
	deleteSQL = `DELETE FROM backup_configuration WHERE id = $1`
)

func scan(r pgx.Row) (*backup.BackupConfig, error) {
	var c backup.BackupConfig
	err := r.Scan(&c.ID, &c.Hour, &c.Minute, &c.LastAutomaticBackupAt, &c.CreatedAt)
	return &c, err
}

func (r *postgresRepository) List(ctx context.Context) ([]backup.BackupConfig, error) {
	var out []backup.BackupConfig
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, listSQL)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scan(rows)
			if err != nil {
				return err
			}
			out = append(out, *c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *postgresRepository) Create(ctx context.Context, c *backup.BackupConfig) (*backup.BackupConfig, error) {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, createSQL, c.ID, c.Hour, c.Minute).Scan(&c.CreatedAt)
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (r *postgresRepository) Get(ctx context.Context, id string) (*backup.BackupConfig, error) {
	var c *backup.BackupConfig
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		c, err = scan(tx.QueryRow(ctx, getSQL, id))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, backup.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (r *postgresRepository) Update(ctx context.Context, c *backup.BackupConfig) (*backup.BackupConfig, error) {
	var affected int64
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, updateSQL, c.Hour, c.Minute, c.ID)
		if err != nil {
			return err
		}
		affected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, backup.ErrNotFound
	}
	return c, nil
}

func (r *postgresRepository) Delete(ctx context.Context, id string) error {
	var affected int64
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, deleteSQL, id)
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
		return backup.ErrNotFound
	}
	return nil
}
