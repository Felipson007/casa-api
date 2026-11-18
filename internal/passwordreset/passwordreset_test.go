package passwordreset

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	usersByEmail map[string]uuid.UUID // lowercase email -> user id
	passwordHash map[uuid.UUID]string
	tokensByID   map[uuid.UUID]Token
	tokensByHash map[string]uuid.UUID
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		usersByEmail: make(map[string]uuid.UUID),
		passwordHash: make(map[uuid.UUID]string),
		tokensByID:   make(map[uuid.UUID]Token),
		tokensByHash: make(map[string]uuid.UUID),
	}
}

func (f *fakeRepository) FindUserByEmail(_ context.Context, email string) (*uuid.UUID, error) {
	id, ok := f.usersByEmail[toLower(email)]
	if !ok {
		return nil, nil
	}
	return &id, nil
}

func (f *fakeRepository) Create(_ context.Context, t Token) error {
	f.tokensByID[t.ID] = t
	f.tokensByHash[t.TokenHash] = t.ID
	return nil
}

func (f *fakeRepository) FindByTokenHash(_ context.Context, tokenHash string) (*Token, error) {
	id, ok := f.tokensByHash[tokenHash]
	if !ok {
		return nil, nil
	}
	t := f.tokensByID[id]
	return &t, nil
}

func (f *fakeRepository) ConsumeToken(_ context.Context, tokenID, userID uuid.UUID, newPasswordHash string, usedAt time.Time) error {
	t, ok := f.tokensByID[tokenID]
	if !ok {
		return errors.New("not found")
	}
	t.UsedAt = &usedAt
	f.tokensByID[tokenID] = t
	f.passwordHash[userID] = newPasswordHash
	return nil
}

func toLower(s string) string {
	// Minimal, dependency-free lowercasing sufficient for test emails (ASCII only).
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func TestService_RequestReset_SilentForUnknownEmail(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	token, err := service.RequestReset(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("expected no error for an unknown email, got %v", err)
	}
	if token != nil {
		t.Fatalf("expected a nil token for an unknown email (no leak), got %v", *token)
	}
	// F4: the unknown-email path still generates+hashes a token internally (constant work), but
	// must persist nothing.
	if len(repo.tokensByID) != 0 {
		t.Fatalf("expected no token persisted for an unknown email, got %d", len(repo.tokensByID))
	}
}

func TestService_RequestReset_GeneratesTokenForKnownEmail(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	userID := uuid.New()
	repo.usersByEmail["user@example.com"] = userID

	token, err := service.RequestReset(ctx, "User@Example.com") // case-insensitive
	if err != nil {
		t.Fatalf("RequestReset returned error: %v", err)
	}
	if token == nil || *token == "" {
		t.Fatal("expected a non-empty raw token for a known email")
	}
	if len(repo.tokensByID) != 1 {
		t.Fatalf("expected 1 token persisted, got %d", len(repo.tokensByID))
	}
	for _, stored := range repo.tokensByID {
		if stored.TokenHash == *token {
			t.Fatal("expected the stored TokenHash to differ from the raw token (must never store it in plaintext)")
		}
		if stored.AppUserID != userID {
			t.Fatalf("expected token tied to user %v, got %v", userID, stored.AppUserID)
		}
	}
}

func TestService_ResetPassword_Succeeds(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	userID := uuid.New()
	repo.usersByEmail["user@example.com"] = userID

	rawToken, err := service.RequestReset(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("RequestReset returned error: %v", err)
	}

	if err := service.ResetPassword(ctx, *rawToken, "correct-horse-battery"); err != nil {
		t.Fatalf("ResetPassword returned error: %v", err)
	}

	storedHash := repo.passwordHash[userID]
	if storedHash == "" {
		t.Fatal("expected a password hash to have been stored")
	}
	if storedHash == "correct-horse-battery" {
		t.Fatal("expected the password to be hashed, not stored in plaintext")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte("correct-horse-battery")); err != nil {
		t.Fatalf("expected the stored hash to verify against the new password: %v", err)
	}

	// The token must now be used and rejected on a second attempt.
	err = service.ResetPassword(ctx, *rawToken, "another-password-123")
	if !isValidationErr(err) {
		t.Fatalf("expected a validation error reusing an already-consumed token, got %v", err)
	}
}

func TestService_ResetPassword_RejectsUnknownToken(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	err := service.ResetPassword(ctx, "not-a-real-token", "some-new-password")
	if !isValidationErr(err) {
		t.Fatalf("expected a validation error for an unknown token, got %v", err)
	}
}

func TestService_ResetPassword_RejectsExpiredToken(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	userID := uuid.New()
	repo.usersByEmail["user@example.com"] = userID

	rawToken, err := service.RequestReset(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("RequestReset returned error: %v", err)
	}

	// Manually expire the token, as if an hour had already passed.
	for id, tok := range repo.tokensByID {
		tok.ExpiresAt = time.Now().Add(-time.Minute)
		repo.tokensByID[id] = tok
	}

	err = service.ResetPassword(ctx, *rawToken, "some-new-password")
	if !isValidationErr(err) {
		t.Fatalf("expected a validation error for an expired token, got %v", err)
	}
}

func isValidationErr(err error) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Kind == apperr.KindValidation
}
