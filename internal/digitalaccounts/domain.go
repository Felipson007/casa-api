// Package digitalaccounts is the digital-account inventory module (GitHub, banks, streaming, ...)
// — added 2026-08-22 per docs/gap-analysis-vida-digital-20260822.md GAP 1. No Java counterpart;
// this is a new module, not a port.
//
// Deliberate design rule: this module NEVER stores a secret. There is no password/token/api_key
// field on Account and none should ever be added — see the migration's own comment
// (internal/migrations/000009_digital_accounts.up.sql). Atlas records WHERE an account lives and
// HOW it's protected/recovered; the actual credential belongs in a password manager.
package digitalaccounts

import (
	"time"

	"github.com/google/uuid"
)

// Category mirrors the digital_accounts.category CHECK constraint.
type Category string

const (
	CategoryDevelopment Category = "DEVELOPMENT"
	CategoryBanking     Category = "BANKING"
	CategoryGovernment  Category = "GOVERNMENT"
	CategoryStreaming   Category = "STREAMING"
	CategoryShopping    Category = "SHOPPING"
	CategorySocial      Category = "SOCIAL"
	CategoryGaming      Category = "GAMING"
	CategoryCloud       Category = "CLOUD"
	CategoryOther       Category = "OTHER"
)

// MFAMethod mirrors the digital_accounts.mfa_method CHECK constraint.
type MFAMethod string

const (
	MFANone    MFAMethod = "NONE"
	MFAPasskey MFAMethod = "PASSKEY"
	MFATOTP    MFAMethod = "TOTP"
	MFASMS     MFAMethod = "SMS"
	MFAApp     MFAMethod = "APP"
	MFAEmail   MFAMethod = "EMAIL"
)

// Status mirrors the digital_accounts.status CHECK constraint.
type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusInactive Status = "INACTIVE"
	StatusDeleted  Status = "DELETED"
)

// ImportOutcome is the per-item result of a bulk import (POST /api/digital-accounts/import): the
// account was either created or skipped because the Atlas already knows it (same service + email).
type ImportOutcome string

const (
	ImportOutcomeCreated ImportOutcome = "CREATED"
	ImportOutcomeSkipped ImportOutcome = "SKIPPED_DUPLICATE"
)

// Account mirrors the digital_accounts table (internal/migrations/000009_digital_accounts.up.sql).
// PersonID is a nullable FK with ON DELETE SET NULL — an account isn't always tied to one specific
// family member. LastReviewedAt is nil until the first POST .../mark-reviewed call; it backs the
// "N não revisadas" count on the Mapa da Vida (GAP 6).
type Account struct {
	ID             uuid.UUID
	PersonID       *uuid.UUID
	ServiceName    string
	Category       Category
	EmailUsed      *string
	MFAMethod      MFAMethod
	RecoveryNotes  *string
	Status         Status
	LastReviewedAt *time.Time
	CreatedAt      time.Time
}
