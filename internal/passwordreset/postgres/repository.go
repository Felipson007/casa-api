// Package postgres implements passwordreset.Repository — the only file in the passwordreset
// module that imports a SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/passwordreset"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ passwordreset.Repository = (*Repository)(nil)

func (r *Repository) FindUserByEmail(ctx context.Context, email string) (*uuid.UUID, error) {
	var result *uuid.UUID
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM app_users WHERE lower(email) = lower($1)`, email).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("looking up app_user by email: %w", err)
		}
		result = &id
		return nil
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, t passwordreset.Token) error {
	return db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO password_reset_tokens (id, app_user_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4)
		`, t.ID, t.AppUserID, t.TokenHash, t.ExpiresAt)
		if err != nil {
			return fmt.Errorf("inserting password_reset_token: %w", err)
		}
		return nil
	})
}

func (r *Repository) FindByTokenHash(ctx context.Context, tokenHash string) (*passwordreset.Token, error) {
	var result *passwordreset.Token
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, app_user_id, token_hash, expires_at, used_at, created_at
			FROM password_reset_tokens
			WHERE token_hash = $1
		`, tokenHash)
		var t passwordreset.Token
		err := row.Scan(&t.ID, &t.AppUserID, &t.TokenHash, &t.ExpiresAt, &t.UsedAt, &t.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("scanning password_reset_token: %w", err)
		}
		result = &t
		return nil
	})
	return result, err
}

func (r *Repository) ConsumeToken(ctx context.Context, tokenID, userID uuid.UUID, newPasswordHash string, usedAt time.Time) error {
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE app_users SET password_hash = $2 WHERE id = $1`, userID, newPasswordHash); err != nil {
			return fmt.Errorf("updating password_hash: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE password_reset_tokens SET used_at = $2 WHERE id = $1`, tokenID, usedAt); err != nil {
			return fmt.Errorf("marking token used: %w", err)
		}
		// Revoke every existing session in the same transaction as the password change — otherwise
		// a session opened before the reset (e.g. by whoever the reset was needed because of) keeps
		// working right through it. Same table auth.store.createSession writes to; deleting here
		// directly (rather than through an auth-package call) keeps this atomic with the two updates
		// above without introducing a cross-module dependency for a single DELETE.
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE app_user_id = $1`, userID); err != nil {
			return fmt.Errorf("revoking sessions: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("consuming password reset token: %w", err)
	}
	return nil
}
