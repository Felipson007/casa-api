// Package postgres implements chat.Repository — the only file in the chat module that imports a
// SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/chat"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ chat.Repository = (*Repository)(nil)

func (r *Repository) List(ctx context.Context, appUserID uuid.UUID) ([]chat.Message, error) {
	var result []chat.Message
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, app_user_id, role, content, model_used, tool_calls, created_at
			FROM chat_messages
			WHERE app_user_id = $1
			ORDER BY created_at ASC
		`, appUserID)
		if err != nil {
			return fmt.Errorf("querying chat_messages: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var m chat.Message
			if err := rows.Scan(&m.ID, &m.AppUserID, &m.Role, &m.Content, &m.ModelUsed, &m.ToolCalls, &m.CreatedAt); err != nil {
				return fmt.Errorf("scanning chat_messages row: %w", err)
			}
			result = append(result, m)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, m chat.Message) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO chat_messages (id, app_user_id, role, content, model_used, tool_calls, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, m.ID, m.AppUserID, m.Role, m.Content, m.ModelUsed, m.ToolCalls, m.CreatedAt)
		if err != nil {
			return fmt.Errorf("inserting chat_message: %w", err)
		}
		return nil
	})
}

func (r *Repository) DeleteAll(ctx context.Context, appUserID uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM chat_messages WHERE app_user_id = $1`, appUserID)
		if err != nil {
			return fmt.Errorf("clearing chat_messages: %w", err)
		}
		return nil
	})
}
