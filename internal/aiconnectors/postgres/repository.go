// Package postgres implements aiconnectors.Repository against the tenant schema. Every query runs
// through db.WithTx so the transaction-scoped tenant search_path applies.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/aiconnectors"
	"github.com/atlas/atlas-go/internal/pandora"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

var _ aiconnectors.Repository = (*Repository)(nil)

const cols = `id, provider, label, base_url, api_key_encrypted, auth_style, chat_model, extraction_model, priority, enabled, created_at, updated_at`

func scan(row pgx.Row) (aiconnectors.Connector, error) {
	var c aiconnectors.Connector
	var provider, authStyle string
	err := row.Scan(&c.ID, &provider, &c.Label, &c.BaseURL, &c.APIKeyEncrypted, &authStyle,
		&c.ChatModel, &c.ExtractionModel, &c.Priority, &c.Enabled, &c.CreatedAt, &c.UpdatedAt)
	c.Provider = aiconnectors.Provider(provider)
	c.AuthStyle = pandora.AuthStyle(authStyle)
	return c, err
}

func (r *Repository) List(ctx context.Context) ([]aiconnectors.Connector, error) {
	var out []aiconnectors.Connector
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+cols+` FROM ai_connectors ORDER BY priority, created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scan(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("aiconnectors: listing: %w", err)
	}
	return out, nil
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (*aiconnectors.Connector, error) {
	var c aiconnectors.Connector
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		c, err = scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM ai_connectors WHERE id = $1`, id))
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("aiconnectors: loading: %w", err)
	}
	return &c, nil
}

func (r *Repository) Create(ctx context.Context, c aiconnectors.Connector) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO ai_connectors (id, provider, label, base_url, api_key_encrypted, auth_style,
			                           chat_model, extraction_model, priority, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			c.ID, string(c.Provider), c.Label, c.BaseURL, c.APIKeyEncrypted, string(c.AuthStyle),
			c.ChatModel, c.ExtractionModel, c.Priority, c.Enabled)
		if err != nil {
			return fmt.Errorf("aiconnectors: inserting: %w", err)
		}
		return nil
	})
}

func (r *Repository) Update(ctx context.Context, c aiconnectors.Connector) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE ai_connectors SET provider=$2, label=$3, base_url=$4, api_key_encrypted=$5,
			       auth_style=$6, chat_model=$7, extraction_model=$8, priority=$9, enabled=$10,
			       updated_at=now()
			WHERE id=$1`,
			c.ID, string(c.Provider), c.Label, c.BaseURL, c.APIKeyEncrypted, string(c.AuthStyle),
			c.ChatModel, c.ExtractionModel, c.Priority, c.Enabled)
		if err != nil {
			return fmt.Errorf("aiconnectors: updating: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM ai_connectors WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("aiconnectors: deleting: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
}
