package digitalaccounts

import (
	"time"

	"github.com/google/uuid"
)

// AccountDTO is the JSON shape returned to clients.
type AccountDTO struct {
	ID             uuid.UUID  `json:"id"`
	PersonID       *uuid.UUID `json:"personId,omitempty"`
	ServiceName    string     `json:"serviceName"`
	Category       Category   `json:"category"`
	EmailUsed      *string    `json:"emailUsed,omitempty"`
	MFAMethod      MFAMethod  `json:"mfaMethod"`
	RecoveryNotes  *string    `json:"recoveryNotes,omitempty"`
	Status         Status     `json:"status"`
	LastReviewedAt *time.Time `json:"lastReviewedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// CreateAccountRequest is the JSON body accepted by POST /api/digital-accounts. Status always
// starts ACTIVE — mirrors the DB column default (same idiom as assets.CreateAssetRequest).
// MFAMethod defaults to NONE when omitted (see Service.Create).
type CreateAccountRequest struct {
	PersonID      *uuid.UUID `json:"personId"`
	ServiceName   string     `json:"serviceName" validate:"required"`
	Category      Category   `json:"category" validate:"required,oneof=DEVELOPMENT BANKING GOVERNMENT STREAMING SHOPPING SOCIAL GAMING CLOUD OTHER"`
	EmailUsed     *string    `json:"emailUsed"`
	MFAMethod     MFAMethod  `json:"mfaMethod" validate:"omitempty,oneof=NONE PASSKEY TOTP SMS APP EMAIL"`
	RecoveryNotes *string    `json:"recoveryNotes"`
}

// ImportAccountItem is one account parsed client-side from an export file (browser CSV,
// Bitwarden/1Password JSON, KeePass XML, generic CSV/JSON). It carries metadata only — the
// frontend never sends the password it read from the file, and this struct has nowhere to put one.
// MFAMethod defaults to NONE when omitted, same as CreateAccountRequest.
type ImportAccountItem struct {
	ServiceName   string    `json:"serviceName" validate:"required,max=150"`
	Category      Category  `json:"category" validate:"required,oneof=DEVELOPMENT BANKING GOVERNMENT STREAMING SHOPPING SOCIAL GAMING CLOUD OTHER"`
	EmailUsed     *string   `json:"emailUsed" validate:"omitempty,max=255"`
	MFAMethod     MFAMethod `json:"mfaMethod" validate:"omitempty,oneof=NONE PASSKEY TOTP SMS APP EMAIL"`
	RecoveryNotes *string   `json:"recoveryNotes"`
}

// ImportAccountsRequest is the JSON body accepted by POST /api/digital-accounts/import. The 500-item
// cap keeps a single request bounded; larger files are imported in successive batches by the
// frontend (which also previews/dedups locally before calling).
type ImportAccountsRequest struct {
	Items []ImportAccountItem `json:"items" validate:"required,min=1,max=500,dive"`
}

// ImportItemResultDTO reports what happened to one imported item.
type ImportItemResultDTO struct {
	ServiceName string        `json:"serviceName"`
	EmailUsed   *string       `json:"emailUsed,omitempty"`
	Outcome     ImportOutcome `json:"outcome"`
}

// ImportAccountsResponse is the result of a bulk import. It never fails as a whole on duplicates —
// each item carries its own outcome so the UI can show "3 importadas, 2 já existiam".
type ImportAccountsResponse struct {
	Created int                   `json:"created"`
	Skipped int                   `json:"skipped"`
	Items   []ImportItemResultDTO `json:"items"`
}

// UpdateAccountRequest is the JSON body accepted by PUT /api/digital-accounts/{id}. Status is
// settable here — e.g. transitioning ACTIVE -> INACTIVE/DELETED. LastReviewedAt is NOT settable
// through this endpoint — see POST /api/digital-accounts/{id}/mark-reviewed.
type UpdateAccountRequest struct {
	PersonID      *uuid.UUID `json:"personId"`
	ServiceName   string     `json:"serviceName" validate:"required"`
	Category      Category   `json:"category" validate:"required,oneof=DEVELOPMENT BANKING GOVERNMENT STREAMING SHOPPING SOCIAL GAMING CLOUD OTHER"`
	EmailUsed     *string    `json:"emailUsed"`
	MFAMethod     MFAMethod  `json:"mfaMethod" validate:"required,oneof=NONE PASSKEY TOTP SMS APP EMAIL"`
	RecoveryNotes *string    `json:"recoveryNotes"`
	Status        Status     `json:"status" validate:"required,oneof=ACTIVE INACTIVE DELETED"`
}
