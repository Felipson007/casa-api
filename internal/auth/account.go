package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
)

// UpdateProfile persists the structured first/last name and keeps the legacy single `name` column
// in sync (it stays the denormalized display name every existing reader uses).
func (s *store) UpdateProfile(ctx context.Context, userID uuid.UUID, firstName, lastName string) error {
	display := firstName
	if lastName != "" {
		display = firstName + " " + lastName
	}
	return db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE app_users SET first_name = $2, last_name = NULLIF($3, ''), name = $4 WHERE id = $1`,
			userID, firstName, lastName, display)
		return err
	})
}

// CheckPassword reports whether password matches the user's stored hash. Google-only accounts (no
// hash) always return false.
func (s *store) CheckPassword(ctx context.Context, userID uuid.UUID, password string) (bool, error) {
	var hash *string
	err := db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT password_hash FROM app_users WHERE id = $1`, userID).Scan(&hash)
	})
	if err != nil {
		return false, fmt.Errorf("auth: loading password hash: %w", err)
	}
	if hash == nil {
		return false, nil
	}
	return bcrypt.CompareHashAndPassword([]byte(*hash), []byte(password)) == nil, nil
}

// ChangePassword sets a new password hash. When the account already has a password, current MUST
// match it first (this is how someone with a stolen session is stopped from locking the real owner
// out). A Google-only account has no password to prove, so current is ignored — the same rule the
// old UI applied.
func (s *store) ChangePassword(ctx context.Context, userID uuid.UUID, current, newPassword string) error {
	var hash *string
	err := db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT password_hash FROM app_users WHERE id = $1`, userID).Scan(&hash)
	})
	if err != nil {
		return fmt.Errorf("auth: loading password hash: %w", err)
	}
	if hash != nil {
		if bcrypt.CompareHashAndPassword([]byte(*hash), []byte(current)) != nil {
			return apperr.Unauthorized("current password is incorrect")
		}
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return apperr.Internal(err)
	}
	return db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE app_users SET password_hash = $2 WHERE id = $1`, userID, string(newHash))
		return err
	})
}

// SetMFASecret stores a pending (not yet active) encrypted TOTP secret, replacing any previous
// un-activated attempt. Activation is a separate step so a half-finished enrollment can never lock
// a user out.
func (s *store) SetMFASecret(ctx context.Context, userID uuid.UUID, encrypted []byte) error {
	return db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE app_users SET mfa_secret = $2, mfa_enabled = false WHERE id = $1`,
			userID, encrypted)
		return err
	})
}

// MFASecret returns the stored encrypted secret (nil when none) and whether MFA is active.
func (s *store) MFASecret(ctx context.Context, userID uuid.UUID) (encrypted []byte, enabled bool, err error) {
	err = db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT mfa_secret, mfa_enabled FROM app_users WHERE id = $1`, userID).
			Scan(&encrypted, &enabled)
	})
	if err != nil {
		return nil, false, fmt.Errorf("auth: loading MFA secret: %w", err)
	}
	return encrypted, enabled, nil
}

// ActivateMFA flips MFA on and installs a fresh set of recovery-code hashes, in one transaction —
// the previously pending secret is what activation truly means, so it is never cleared.
func (s *store) ActivateMFA(ctx context.Context, userID uuid.UUID, recoveryHashes []string) error {
	return db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE app_users SET mfa_enabled = true, mfa_enabled_at = now() WHERE id = $1`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE app_user_id = $1`, userID); err != nil {
			return err
		}
		for _, h := range recoveryHashes {
			if _, err := tx.Exec(ctx,
				`INSERT INTO mfa_recovery_codes (app_user_id, code_hash) VALUES ($1, $2)`, userID, h); err != nil {
				return err
			}
		}
		return nil
	})
}

// DisableMFA clears the secret and every recovery code and turns MFA off.
func (s *store) DisableMFA(ctx context.Context, userID uuid.UUID) error {
	return db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE app_users SET mfa_enabled = false, mfa_secret = NULL, mfa_enabled_at = NULL WHERE id = $1`,
			userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE app_user_id = $1`, userID)
		return err
	})
}

// ReplaceRecoveryCodes swaps in a new set (used when the user regenerates them).
func (s *store) ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, recoveryHashes []string) error {
	return db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE app_user_id = $1`, userID); err != nil {
			return err
		}
		for _, h := range recoveryHashes {
			if _, err := tx.Exec(ctx,
				`INSERT INTO mfa_recovery_codes (app_user_id, code_hash) VALUES ($1, $2)`, userID, h); err != nil {
				return err
			}
		}
		return nil
	})
}

// ConsumeRecoveryCode marks a matching, unused recovery code as used and reports whether one was
// found — single-use by construction (the WHERE excludes already-used rows).
func (s *store) ConsumeRecoveryCode(ctx context.Context, userID uuid.UUID, codeHash string) (bool, error) {
	var used bool
	err := db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE mfa_recovery_codes SET used_at = now()
			 WHERE app_user_id = $1 AND code_hash = $2 AND used_at IS NULL`,
			userID, codeHash)
		if err != nil {
			return err
		}
		used = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("auth: consuming recovery code: %w", err)
	}
	return used, nil
}

// MFAChallenge is the short-lived handoff between a correct password and a verified second factor.
type MFAChallenge struct {
	ID        uuid.UUID
	AppUserID uuid.UUID
}

// CreateMFAChallenge issues a fresh challenge token for a user whose password just checked out.
func (s *store) CreateMFAChallenge(ctx context.Context, userID uuid.UUID) (raw string, expiresAt time.Time, err error) {
	raw, err = generateMFAChallengeToken()
	if err != nil {
		return "", time.Time{}, apperr.Internal(err)
	}
	expiresAt = time.Now().Add(mfaChallengeValidity)
	err = db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO mfa_challenges (app_user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
			userID, hashMFAChallenge(raw), expiresAt)
		return err
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: creating MFA challenge: %w", err)
	}
	return raw, expiresAt, nil
}

// FindMFAChallenge resolves a raw challenge token to its row, or (nil, nil) when it is unknown,
// used, or expired — a single generic answer for every failure, like session lookups.
func (s *store) FindMFAChallenge(ctx context.Context, raw string) (*MFAChallenge, error) {
	var c MFAChallenge
	err := db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, app_user_id FROM mfa_challenges
			WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()`,
			hashMFAChallenge(raw)).Scan(&c.ID, &c.AppUserID)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("auth: finding MFA challenge: %w", err)
	}
	return &c, nil
}

// MarkMFAChallengeUsed consumes a challenge after its second factor verified.
func (s *store) MarkMFAChallengeUsed(ctx context.Context, id uuid.UUID) error {
	return db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE mfa_challenges SET used_at = now() WHERE id = $1`, id)
		return err
	})
}
