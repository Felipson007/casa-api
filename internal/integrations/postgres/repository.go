// Package postgres implements integrations.Repository — the only file in the integrations module
// that imports a SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/integrations"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ integrations.Repository = (*Repository)(nil)

func (r *Repository) ListExternal(ctx context.Context) ([]integrations.ExternalIntegration, error) {
	var result []integrations.ExternalIntegration
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, type, connected_account, refresh_token_encrypted, refresh_token_iv, models,
			       last_telegram_update_id, telegram_pairing_code, telegram_pairing_expires_at,
			       created_at
			FROM external_integrations
			ORDER BY type
		`)
		if err != nil {
			return fmt.Errorf("querying external_integrations: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			e, err := scanExternal(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindExternalByType(ctx context.Context, t integrations.Type) (*integrations.ExternalIntegration, error) {
	var result *integrations.ExternalIntegration
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, type, connected_account, refresh_token_encrypted, refresh_token_iv, models,
			       last_telegram_update_id, telegram_pairing_code, telegram_pairing_expires_at,
			       created_at
			FROM external_integrations
			WHERE type = $1
			ORDER BY created_at DESC
			LIMIT 1
		`, t)
		e, err := scanExternal(row)
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

// UpsertExternal inserts a new row, or replaces the existing one with the same id — the caller
// (Service.Connect) always resolves the id first (reusing the existing row's id on a reconnect,
// generating a fresh one otherwise), so ON CONFLICT(id) is enough; there's no DB-level
// UNIQUE(type) to conflict on instead (see Repository interface doc).
func (r *Repository) UpsertExternal(ctx context.Context, e integrations.ExternalIntegration) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO external_integrations (id, type, connected_account, refresh_token_encrypted,
			                                    refresh_token_iv, models, last_telegram_update_id,
			                                    telegram_pairing_code, telegram_pairing_expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (id) DO UPDATE SET
				type = EXCLUDED.type,
				connected_account = EXCLUDED.connected_account,
				refresh_token_encrypted = EXCLUDED.refresh_token_encrypted,
				refresh_token_iv = EXCLUDED.refresh_token_iv,
				models = EXCLUDED.models,
				last_telegram_update_id = EXCLUDED.last_telegram_update_id,
				telegram_pairing_code = EXCLUDED.telegram_pairing_code,
				telegram_pairing_expires_at = EXCLUDED.telegram_pairing_expires_at
		`, e.ID, e.Type, e.ConnectedAccount, e.RefreshTokenEncrypted, e.RefreshTokenIV, e.Models,
			e.LastTelegramUpdateID, e.TelegramPairingCode, e.TelegramPairingExpiresAt)
		if err != nil {
			return fmt.Errorf("upserting external_integration: %w", err)
		}
		return nil
	})
}

func (r *Repository) DeleteExternalByType(ctx context.Context, t integrations.Type) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM external_integrations WHERE type = $1`, t)
		if err != nil {
			return fmt.Errorf("deleting external_integration: %w", err)
		}
		return nil
	})
}

func (r *Repository) GetSystemStatus(ctx context.Context) (*integrations.SystemIntegration, error) {
	var result *integrations.SystemIntegration
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, type, connected_account, refresh_token_encrypted, refresh_token_iv, created_at
			FROM system_integrations
			ORDER BY created_at DESC
			LIMIT 1
		`)
		var s integrations.SystemIntegration
		err := row.Scan(&s.ID, &s.Type, &s.ConnectedAccount, &s.RefreshTokenEncrypted, &s.RefreshTokenIV, &s.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("scanning system_integrations row: %w", err)
		}
		result = &s
		return nil
	})
	return result, err
}

func (r *Repository) DeleteSystemByType(ctx context.Context, t integrations.Type) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM system_integrations WHERE type = $1`, t)
		if err != nil {
			return fmt.Errorf("deleting system_integration: %w", err)
		}
		return nil
	})
}

// rowScanner is satisfied by both pgx.Rows and pgx.Row.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanExternal(row rowScanner) (integrations.ExternalIntegration, error) {
	var e integrations.ExternalIntegration
	err := row.Scan(&e.ID, &e.Type, &e.ConnectedAccount, &e.RefreshTokenEncrypted, &e.RefreshTokenIV,
		&e.Models, &e.LastTelegramUpdateID, &e.TelegramPairingCode, &e.TelegramPairingExpiresAt,
		&e.CreatedAt)
	return e, err
}
