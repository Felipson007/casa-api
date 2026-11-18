// Package passwordreset is the "forgot password" token module (password_reset_tokens in the old
// app) — owned by Claude, carved out of Hermes's broader auth ownership per the D-005 task split
// (chat msg 91/94: "password_reset_tokens: schema ja existe no 000001, entao e so codigo Go").
//
// SCOPE NOTE: persistence + the token lifecycle only, no email delivery — there is no email
// module anywhere in this codebase yet. RequestReset generates and stores a token but never sends
// anything; the raw (plaintext) token is returned to its caller so a FUTURE email-sending layer
// can build the reset link, but this module's own HTTP handler deliberately never puts it in the
// response body. The raw token is the literal proof of identity ResetPassword accepts — the
// entire reason TokenHash exists (never the raw token) is that once generated, it must only ever
// reach the user through the one channel (email) that doesn't exist yet, never back out over the
// API.
//
// WIRING NOTE (Hermes-owned, cmd/api/main.go): both HTTP endpoints this module registers must be
// mounted on the PUBLIC mux (same one as /health), not the authenticated module mux — a user
// requesting a password reset is, by definition, not logged in yet. See routes.go's doc comment.
package passwordreset

import (
	"time"

	"github.com/google/uuid"
)

// Token mirrors the password_reset_tokens table (internal/migrations/000001_init_schema.up.sql).
// TokenHash is a SHA-256 hash of the token actually given to the user — never the raw token
// itself, same reasoning as app_users.password_hash (a DB dump alone must never be enough to
// reset anyone's password). UsedAt is nil until consumed — a token is single-use even if still
// within ExpiresAt.
type Token struct {
	ID        uuid.UUID
	AppUserID uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}
