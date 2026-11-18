//go:build integration

package passwordreset_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/atlas/atlas-go/internal/passwordreset"
	"github.com/atlas/atlas-go/internal/passwordreset/postgres"
)

// TestRepository_RequestAndResetPassword exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/passwordreset/...
//
// against TEST_DATABASE_URL (never DATABASE_URL). Requires a real app_users row.
func TestRepository_RequestAndResetPassword(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()

	email := "passwordreset-integration-test@example.com"
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_users (email, role) VALUES ($1, 'MEMBER') RETURNING id`, email,
	).Scan(&userID); err != nil {
		t.Fatalf("seeding test app_user: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM app_users WHERE id = $1`, userID) //nolint:errcheck

	// A session opened before the reset must not survive it — see ConsumeToken's session-revocation.
	if _, err := pool.Exec(ctx,
		`INSERT INTO sessions (app_user_id, token_hash, expires_at) VALUES ($1, 'irrelevant-hash', now() + interval '1 day')`,
		userID,
	); err != nil {
		t.Fatalf("seeding a pre-reset session: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM sessions WHERE app_user_id = $1`, userID) //nolint:errcheck

	repo := postgres.NewRepository(pool)
	service := passwordreset.NewService(repo)

	// Unknown email: silent no-op, no row created.
	nilToken, err := service.RequestReset(ctx, "definitely-not-registered@example.com")
	if err != nil {
		t.Fatalf("RequestReset (unknown email): %v", err)
	}
	if nilToken != nil {
		t.Fatal("expected a nil token for an unknown email")
	}

	rawToken, err := service.RequestReset(ctx, email)
	if err != nil {
		t.Fatalf("RequestReset: %v", err)
	}
	if rawToken == nil || *rawToken == "" {
		t.Fatal("expected a non-empty raw token")
	}
	defer pool.Exec(ctx, `DELETE FROM password_reset_tokens WHERE app_user_id = $1`, userID) //nolint:errcheck

	var storedHash string
	if err := pool.QueryRow(ctx, `SELECT token_hash FROM password_reset_tokens WHERE app_user_id = $1`, userID).Scan(&storedHash); err != nil {
		t.Fatalf("reading stored token_hash: %v", err)
	}
	if storedHash == *rawToken {
		t.Fatal("expected token_hash to differ from the raw token — must never store it in plaintext")
	}

	if err := service.ResetPassword(ctx, *rawToken, "correct-horse-battery-staple"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	var passwordHash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM app_users WHERE id = $1`, userID).Scan(&passwordHash); err != nil {
		t.Fatalf("reading updated password_hash: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte("correct-horse-battery-staple")); err != nil {
		t.Fatalf("expected the stored password_hash to verify against the new password: %v", err)
	}

	var usedAt *string
	if err := pool.QueryRow(ctx, `SELECT used_at::text FROM password_reset_tokens WHERE app_user_id = $1`, userID).Scan(&usedAt); err != nil {
		t.Fatalf("reading used_at: %v", err)
	}
	if usedAt == nil {
		t.Fatal("expected used_at to be set after consuming the token")
	}

	// Reusing the same token must now fail.
	if err := service.ResetPassword(ctx, *rawToken, "another-password-1234"); err == nil {
		t.Fatal("expected reusing an already-consumed token to fail")
	}

	var remainingSessions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE app_user_id = $1`, userID).Scan(&remainingSessions); err != nil {
		t.Fatalf("counting remaining sessions: %v", err)
	}
	if remainingSessions != 0 {
		t.Fatalf("expected every pre-reset session to be revoked, found %d remaining", remainingSessions)
	}
}
