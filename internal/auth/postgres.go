// Package auth holds the postgres-backed implementations of the authz boundary's two injected
// functions (LoadUserFunc and CheckPermFunc), defined in internal/platform/httpx/authz.go. This is
// the only file that imports a SQL driver for auth — httpx itself stays driver-free (D-003: single
// shared authz boundary, no per-module reimplementation).
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

type store struct {
	pool *pgxpool.Pool
}

// New builds the two authz functions from a pool. LoadUserBySession resolves a session token
// (see session.go — replaces the old raw-user-id-as-bearer-token placeholder); CheckPerm applies
// the OWNER-bypass rule and falls back to member_permissions for MEMBERs.
func New(pool *pgxpool.Pool) (httpx.LoadUserFunc, httpx.CheckPermFunc) {
	s := &store{pool: pool}
	return s.LoadUserBySession, s.CheckPerm
}

// Store exposes the auth store's methods beyond the two httpx hooks (login, logout, profile,
// permissions) — used by the handler in this package. Returned by NewStore so cmd/api/main.go can
// wire the HTTP handler without reaching into an unexported type.
type Store interface {
	AuthenticatePassword(ctx context.Context, email, password string) (uuid.UUID, error)
	LoginWithPassword(ctx context.Context, email, password string) (rawToken string, expiresAt time.Time, err error)
	AuthenticateGoogleEmail(ctx context.Context, email string) (uuid.UUID, error)
	LoginWithGoogleEmail(ctx context.Context, email string) (rawToken string, expiresAt time.Time, err error)
	CreateSession(ctx context.Context, userID uuid.UUID) (rawToken string, expiresAt time.Time, err error)
	LoadUserBySession(ctx context.Context, rawToken string) (httpx.User, error)
	Logout(ctx context.Context, rawToken string) error
	FindProfile(ctx context.Context, userID uuid.UUID) (*UserProfile, error)
	PermissionsForUser(ctx context.Context, userID uuid.UUID) (map[string]ModulePermission, error)

	// Self-service account security (see account.go).
	UpdateProfile(ctx context.Context, userID uuid.UUID, firstName, lastName string) error
	CheckPassword(ctx context.Context, userID uuid.UUID, password string) (bool, error)
	ChangePassword(ctx context.Context, userID uuid.UUID, current, newPassword string) error
	SetMFASecret(ctx context.Context, userID uuid.UUID, encrypted []byte) error
	MFASecret(ctx context.Context, userID uuid.UUID) (encrypted []byte, enabled bool, err error)
	ActivateMFA(ctx context.Context, userID uuid.UUID, recoveryHashes []string) error
	DisableMFA(ctx context.Context, userID uuid.UUID) error
	ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, recoveryHashes []string) error
	ConsumeRecoveryCode(ctx context.Context, userID uuid.UUID, codeHash string) (bool, error)
	CreateMFAChallenge(ctx context.Context, userID uuid.UUID) (raw string, expiresAt time.Time, err error)
	FindMFAChallenge(ctx context.Context, raw string) (*MFAChallenge, error)
	MarkMFAChallengeUsed(ctx context.Context, id uuid.UUID) error
}

// NewStore builds the Store used by Handler — separate from New so cmd/api/main.go can share one
// underlying pool-backed store for both the authz hooks and the HTTP handler.
func NewStore(pool *pgxpool.Pool) Store {
	return &store{pool: pool}
}

// CheckPerm: OWNER always allowed. MEMBER must have a member_permissions row with the matching
// can_read (PermRead) or can_write (PermWrite) flag. Missing row = denied.
func (s *store) CheckPerm(ctx context.Context, u httpx.User, module string, mode httpx.PermMode) (bool, error) {
	if u.Role == "OWNER" {
		return true, nil
	}
	if u.Role != "MEMBER" {
		return false, nil
	}
	var col string
	switch mode {
	case httpx.PermRead:
		col = "can_read"
	case httpx.PermWrite:
		col = "can_write"
	default:
		return false, nil
	}
	var allowed bool
	err := db.WithControlTx(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+col+` FROM member_permissions WHERE user_id = $1 AND module = $2`, u.ID, module)
		// No row -> sql.ErrNoRows -> default false, which is the correct "no access" answer.
		return row.Scan(&allowed)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return allowed, nil
}
