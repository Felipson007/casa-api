package passwordreset

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// tokenValidity mirrors the old Java's VALIDADE — long enough to act on the email, short enough
// that an old, unused link stops being a standing risk. Same order of magnitude every major
// provider uses.
const tokenValidity = time.Hour

// tokenRandomBytes mirrors the old Java's gerarTokenAleatorio — 32 random bytes is enough entropy
// that hashing it for storage doesn't need to be slow/salted (see hashToken's doc comment).
const tokenRandomBytes = 32

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	// FindUserByEmail returns the app_users.id matching email (case-insensitive), or nil if none
	// exists. Deliberately never an error for "not found" — see Service.RequestReset's doc
	// comment on why an unknown email is a silent no-op, not a 404.
	FindUserByEmail(ctx context.Context, email string) (*uuid.UUID, error)
	Create(ctx context.Context, t Token) error
	// FindByTokenHash returns the token row matching a SHA-256 hash, or nil if none exists.
	FindByTokenHash(ctx context.Context, tokenHash string) (*Token, error)
	// ConsumeToken marks the token used and updates the user's password_hash, in one transaction
	// — mirrors the old Java's single @Transactional redefinir, which saves both the AppUser and
	// the PasswordResetToken together; splitting this into two Repository calls would reopen a
	// partial-failure window (token marked used but password never actually changed, or the
	// reverse) the Java never had.
	ConsumeToken(ctx context.Context, tokenID, userID uuid.UUID, newPasswordHash string, usedAt time.Time) error
}

// Service holds the business rules.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// RequestReset generates and stores a single-use reset token for the app_user matching email, if
// one exists. Deliberately silent (nil, nil — no error, no indication either way) when the email
// doesn't match any user: the caller (HTTP handler) must respond with the exact same generic
// message regardless, so this being a no-op for an unknown email is what keeps the response from
// leaking which emails have an account (mirrors the old Java PasswordResetService.solicitar's
// javadoc exactly).
//
// The returned raw token is the plaintext value that would go in the emailed link — never stored
// anywhere (only its SHA-256 hash is persisted) and never something this module's own HTTP
// handler puts in a response body. It exists purely for a future email-sending layer to consume;
// until that exists, callers other than that future layer should discard it, not log it.
func (s *Service) RequestReset(ctx context.Context, email string) (rawToken *string, err error) {
	userID, err := s.repo.FindUserByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("passwordreset: looking up user by email: %w", err)
	}

	// Generate + hash the token regardless of whether the account exists, so the unknown-email
	// path does the same work as the known-email one up to (and excluding) the DB insert — one
	// less signal for account enumeration (audit F4). Rate limiting on this endpoint remains the
	// primary mitigation and is tracked separately.
	raw, err := generateRawToken()
	if err != nil {
		return nil, apperr.Internal(err)
	}
	tokenHash := hashToken(raw)

	if userID == nil {
		return nil, nil
	}

	t := Token{
		ID:        uuid.New(),
		AppUserID: *userID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(tokenValidity),
	}
	if err := s.repo.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("passwordreset: creating token: %w", err)
	}
	return &raw, nil
}

// ResetPassword exchanges a valid, unexpired, unused token for setting a new password — mirrors
// PasswordResetService.redefinir. Unlike RequestReset, an invalid token IS reported back
// (apperr.Validation): by the time a caller has a token in hand at all, it came from the emailed
// link, so confirming it's wrong/expired doesn't leak anything new the way an "email not found"
// response would at the request-reset step.
func (s *Service) ResetPassword(ctx context.Context, rawToken, newPassword string) error {
	token, err := s.repo.FindByTokenHash(ctx, hashToken(rawToken))
	if err != nil {
		return fmt.Errorf("passwordreset: looking up token: %w", err)
	}
	if token == nil || token.UsedAt != nil || !token.ExpiresAt.After(time.Now()) {
		return apperr.Validation("invalid or expired reset link — request a new one")
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return apperr.Internal(err)
	}

	if err := s.repo.ConsumeToken(ctx, token.ID, token.AppUserID, string(hashed), time.Now()); err != nil {
		return fmt.Errorf("passwordreset: consuming token: %w", err)
	}
	return nil
}

// generateRawToken mirrors gerarTokenAleatorio: 32 cryptographically random bytes, URL-safe
// base64 (no padding) so it drops straight into a query string with no escaping.
func generateRawToken() (string, error) {
	b := make([]byte, tokenRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken mirrors the old Java's reasoning exactly: SHA-256, not bcrypt. The token itself
// already has 32 random bytes of entropy, so a slow, salted hash buys nothing — and a
// deterministic hash is what makes FindByTokenHash possible at all (bcrypt produces a different
// hash every time for the same input, so it can't be looked up by equality).
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.StdEncoding.EncodeToString(sum[:])
}
