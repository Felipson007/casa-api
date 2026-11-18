package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// SessionValidity is how long an issued session stays valid before the browser must log in again.
// 30 days: a private single-tenant household app, convenience over frequent re-auth.
const SessionValidity = 30 * 24 * time.Hour

// sessionTokenRandomBytes mirrors passwordreset's token generation: 32 random bytes is enough
// entropy that hashing it for storage doesn't need to be slow/salted.
const sessionTokenRandomBytes = 32

// dummyPasswordHash is a valid bcrypt hash (of a throwaway value) at the same cost real
// password_hash rows use. LoginWithPassword compares against it when the account is missing or
// has no password, so that path costs the same ~one bcrypt as "real account, wrong password" —
// otherwise response latency is an account-existence / Google-only oracle (audit F4).
var dummyPasswordHash = mustBcrypt("not-a-real-password-timing-equalizer")

func mustBcrypt(s string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(s), bcrypt.DefaultCost)
	if err != nil {
		panic("auth: precomputing dummy password hash: " + err.Error())
	}
	return h
}

// AuthenticatePassword verifies email+password (bcrypt against app_users.password_hash) without
// issuing a session — the first half of Login, split out so the handler can demand a second factor
// before a session exists. A missing account, a missing password_hash (Google-only account), and a
// wrong password all report the same generic error AND take the same time — this must never reveal
// which case it was, by content or by latency.
func (s *store) AuthenticatePassword(ctx context.Context, email, password string) (userID uuid.UUID, err error) {
	var hash *string
	err = db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT id, password_hash FROM app_users WHERE lower(email) = lower($1)`, email)
		return row.Scan(&userID, &hash)
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("auth: looking up user: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) || hash == nil {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
		return uuid.Nil, apperr.Unauthorized("invalid email or password")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*hash), []byte(password)); err != nil {
		return uuid.Nil, apperr.Unauthorized("invalid email or password")
	}
	return userID, nil
}

// LoginWithPassword verifies email+password and issues a new session — the MFA-less path (and the
// behavior every pre-MFA caller and timing test expects).
func (s *store) LoginWithPassword(ctx context.Context, email, password string) (rawToken string, expiresAt time.Time, err error) {
	userID, err := s.AuthenticatePassword(ctx, email, password)
	if err != nil {
		return "", time.Time{}, err
	}
	return s.createSession(ctx, userID)
}

// AuthenticateGoogleEmail resolves a Google-verified email address to an app_user id without
// issuing a session — split out so the OAuth callback can require the second factor before a
// session exists, exactly like the password path. No signup: the account must already exist
// (single-tenant household app, same no-auto-provisioning rule D-012 already applies to
// member_permissions).
func (s *store) AuthenticateGoogleEmail(ctx context.Context, email string) (uuid.UUID, error) {
	var userID uuid.UUID
	err := db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT id FROM app_users WHERE lower(email) = lower($1)`, email)
		return row.Scan(&userID)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, apperr.Forbidden("no account registered for this email")
		}
		return uuid.Nil, fmt.Errorf("auth: looking up user: %w", err)
	}
	return userID, nil
}

// LoginWithGoogleEmail issues a session for the app_user matching a Google-verified email address
// — the MFA-less Google path (and what the existing integration tests use).
func (s *store) LoginWithGoogleEmail(ctx context.Context, email string) (rawToken string, expiresAt time.Time, err error) {
	userID, err := s.AuthenticateGoogleEmail(ctx, email)
	if err != nil {
		return "", time.Time{}, err
	}
	return s.createSession(ctx, userID)
}

// CreateSession issues a session for an already-authenticated user (password verified, and any
// second factor verified) — used by the MFA login path, which cannot go through LoginWithPassword.
func (s *store) CreateSession(ctx context.Context, userID uuid.UUID) (rawToken string, expiresAt time.Time, err error) {
	return s.createSession(ctx, userID)
}

func (s *store) createSession(ctx context.Context, userID uuid.UUID) (rawToken string, expiresAt time.Time, err error) {
	raw, err := generateSessionToken()
	if err != nil {
		return "", time.Time{}, apperr.Internal(err)
	}
	expiresAt = time.Now().Add(SessionValidity)
	err = db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO sessions (app_user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
			userID, hashSessionToken(raw), expiresAt)
		return err
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: creating session: %w", err)
	}
	return raw, expiresAt, nil
}

// LoadUserBySession resolves a raw session token to the authenticated user — the httpx.LoadUserFunc
// implementation once real auth replaced the raw-user-id-as-bearer-token placeholder. An
// expired/unknown token and an expired/deleted user both come back as the zero User (401 at
// AuthMiddleware), never a distinguishing error.
func (s *store) LoadUserBySession(ctx context.Context, rawToken string) (httpx.User, error) {
	var u httpx.User
	err := db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT a.id, a.role,
			       COALESCE(a.tenant_id, '00000000-0000-0000-0000-000000000000'::uuid),
			       COALESCE(t.schema_name, '') FROM sessions s
			JOIN app_users a ON a.id = s.app_user_id
			LEFT JOIN tenants t ON t.id = a.tenant_id
			WHERE s.token_hash = $1 AND s.expires_at > now()`,
			hashSessionToken(rawToken))
		return row.Scan(&u.ID, &u.Role, &u.TenantID, &u.TenantSchema)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.User{}, nil
		}
		return httpx.User{}, err
	}
	return u, nil
}

// Logout deletes the session row matching the raw token — best-effort revocation (a stolen token
// used before logout still works until it expires; acceptable for a private household app with no
// public exposure of session tokens).
func (s *store) Logout(ctx context.Context, rawToken string) error {
	return db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashSessionToken(rawToken))
		return err
	})
}

// ModulePermission is one member_permissions row's read/write flags.
type ModulePermission struct {
	CanRead  bool
	CanWrite bool
}

// PermissionsForUser loads every member_permissions row for a user in one query — used to build
// the /api/auth/me response's per-module flags without one round trip per module.
func (s *store) PermissionsForUser(ctx context.Context, userID uuid.UUID) (map[string]ModulePermission, error) {
	result := make(map[string]ModulePermission)
	err := db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT module, can_read, can_write FROM member_permissions WHERE user_id = $1`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var module string
			var p ModulePermission
			if err := rows.Scan(&module, &p.CanRead, &p.CanWrite); err != nil {
				return err
			}
			result[module] = p
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("auth: loading permissions: %w", err)
	}
	return result, nil
}

// UserProfile is the full identity returned by /api/auth/me.
type UserProfile struct {
	ID          uuid.UUID
	Email       string
	FirstName   *string
	LastName    *string
	Name        *string
	Role        string
	HasPassword bool
	MFAEnabled  bool
}

func (s *store) FindProfile(ctx context.Context, userID uuid.UUID) (*UserProfile, error) {
	var p UserProfile
	var hash *string
	err := db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT id, email, first_name, last_name, name, role, password_hash, mfa_enabled
			 FROM app_users WHERE id = $1`, userID)
		return row.Scan(&p.ID, &p.Email, &p.FirstName, &p.LastName, &p.Name, &p.Role, &hash, &p.MFAEnabled)
	})
	p.HasPassword = hash != nil
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("auth: loading profile: %w", err)
	}
	return &p, nil
}

func generateSessionToken() (string, error) {
	b := make([]byte, sessionTokenRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashSessionToken mirrors passwordreset's hashToken exactly: SHA-256, not bcrypt — the raw token
// already has 32 random bytes of entropy, and a deterministic hash is what makes the session
// lookup possible at all (bcrypt can't be looked up by equality).
func hashSessionToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.StdEncoding.EncodeToString(sum[:])
}
