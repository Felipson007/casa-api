//go:build integration

// Package auth integration tests for the postgres-backed authz functions (LoadUser + CheckPerm),
// exercised against a real Postgres (TEST_DATABASE_URL). Mirrors the shape of the per-module
// integration tests: skip if TEST_DATABASE_URL is unset, create isolated rows, clean up after.
package auth_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/auth"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

func newPool(t *testing.T) *pgxpool.Pool {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// insertUser creates an app_users row with the given role and returns its id. Cleaned up after.
func insertUser(t *testing.T, pool *pgxpool.Pool, role string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO app_users (id, email, role) VALUES ($1, $2, $3)`, id, id.String()+"@test.local", role)
	if err != nil {
		t.Fatalf("inserting test user: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM app_users WHERE id = $1`, id)
	})
	return id
}

// sessionTokenFor logs the given app_user in via Google email (no password needed) to obtain a
// real session token — loadUser is now httpx.LoadUserFunc, keyed by session token, not raw user
// ID (see postgres.go/session.go's doc comments: replaces the old bearer-raw-user-id placeholder).
func sessionTokenFor(t *testing.T, store auth.Store, userID uuid.UUID) string {
	t.Helper()
	token, _, err := store.LoginWithGoogleEmail(context.Background(), userID.String()+"@test.local")
	if err != nil {
		t.Fatalf("logging in test user %s: %v", userID, err)
	}
	return token
}

func TestLoadUser_ReturnsRole(t *testing.T) {
	pool := newPool(t)
	loadUser, _ := auth.New(pool)
	store := auth.NewStore(pool)

	ownerID := insertUser(t, pool, "OWNER")
	memberID := insertUser(t, pool, "MEMBER")

	ou, err := loadUser(context.Background(), sessionTokenFor(t, store, ownerID))
	if err != nil || ou.Role != "OWNER" {
		t.Fatalf("expected OWNER user, got %+v err=%v", ou, err)
	}
	mu, err := loadUser(context.Background(), sessionTokenFor(t, store, memberID))
	if err != nil || mu.Role != "MEMBER" {
		t.Fatalf("expected MEMBER user, got %+v err=%v", mu, err)
	}
}

func TestLoadUser_UnknownReturnsNil(t *testing.T) {
	pool := newPool(t)
	loadUser, _ := auth.New(pool)

	u, err := loadUser(context.Background(), "not-a-real-session-token")
	if err != nil {
		t.Fatalf("expected no error for unknown token, got %v", err)
	}
	if u.ID != uuid.Nil {
		t.Fatalf("expected nil user for unknown token, got %+v", u)
	}
}

func TestCheckPerm_OwnerAlwaysAllowed(t *testing.T) {
	pool := newPool(t)
	_, checkPerm := auth.New(pool)
	owner := httpx.User{ID: uuid.New(), Role: "OWNER"}

	for _, m := range []httpx.PermMode{httpx.PermRead, httpx.PermWrite} {
		ok, err := checkPerm(context.Background(), owner, "expenses", m)
		if err != nil || !ok {
			t.Fatalf("owner should be allowed %s on expenses, got ok=%v err=%v", m, ok, err)
		}
	}
}

func TestCheckPerm_MemberDeniedWithoutRow(t *testing.T) {
	pool := newPool(t)
	_, checkPerm := auth.New(pool)
	memberID := insertUser(t, pool, "MEMBER")
	member := httpx.User{ID: memberID, Role: "MEMBER"}

	ok, err := checkPerm(context.Background(), member, "expenses", httpx.PermWrite)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("member without member_permissions row should be denied")
	}
}

func TestCheckPerm_MemberAllowedWithRow(t *testing.T) {
	pool := newPool(t)
	_, checkPerm := auth.New(pool)
	memberID := insertUser(t, pool, "MEMBER")
	member := httpx.User{ID: memberID, Role: "MEMBER"}
	ctx := context.Background()

	_, err := pool.Exec(ctx, `INSERT INTO member_permissions (user_id, module, can_read, can_write) VALUES ($1, 'expenses', true, true)`, memberID)
	if err != nil {
		t.Fatalf("inserting member_permissions: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM member_permissions WHERE user_id = $1`, memberID)
	})

	readOK, err := checkPerm(ctx, member, "expenses", httpx.PermRead)
	if err != nil || !readOK {
		t.Fatalf("member with row should be allowed read, got ok=%v err=%v", readOK, err)
	}
	writeOK, err := checkPerm(ctx, member, "expenses", httpx.PermWrite)
	if err != nil || !writeOK {
		t.Fatalf("member with row should be allowed write, got ok=%v err=%v", writeOK, err)
	}
}
