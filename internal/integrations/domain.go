// Package integrations is the third-party-connections module (integracoes_externas/
// integracoes_sistema/ia_configuracoes in the old app) — owned by Claude per
// decisions/DECISIONS.md D-005, last module in the priority order (external credentials).
//
// Scope note: this module persists connection status + encrypted-at-rest credentials, plus the
// real Google Drive OAuth authorize/callback flow (see googledrive_oauth.go — added once the
// household actually needed to reconnect Drive against the real homelab deployment). It does NOT
// implement any actual network call to Telegram/Gemini/DeepSeek/Groq/OpenRouter — those
// need real API credentials this module only stores, never uses directly. What IS implemented:
// Connect (paste an API key or bot token, we encrypt and store it — mirrors
// GeminiIntegracaoService's pattern exactly, for the non-OAuth types), the Google Drive OAuth
// connect/callback pair (mirrors GoogleDriveIntegracaoService), Disconnect, and status listing
// that never returns the secret. Google Drive is explicitly rejected by the generic Connect (its
// row can only come from the real OAuth exchange, not a pasted secret) — see
// ConnectExternalIntegrationRequest's validation tag in dto.go.
package integrations

import (
	"time"

	"github.com/google/uuid"
)

// Type mirrors the type CHECK on external_integrations/system_integrations, widened by D-013
// (000002_fix_integration_types) to match the real Java TipoIntegracao 1:1. The original 000001
// CHECK was ('IMMICH', 'TELEGRAM', 'OPENAI', 'OTHER') — OPENAI isn't a real Java value, and OTHER
// would have collapsed 5 genuinely distinct provider types into one indistinguishable bucket.
type Type string

const (
	TypeGoogleDrive Type = "GOOGLE_DRIVE"
	TypeTelegram    Type = "TELEGRAM"
	// TypePluggy is the Open Finance aggregator (see internal/openfinance).
	TypePluggy Type = "PLUGGY"
	// TypeICloudCalendar is the household's iCloud Calendar, synced over CalDAV (see
	// internal/icloudcal). Credentials are an Apple ID + an app-specific password.
	TypeICloudCalendar Type = "ICLOUD_CALENDAR"
	// TypeGoogleCalendar is the household's Google Calendar, synced via the Calendar API (see
	// internal/googlecal). Credentials are an OAuth refresh token + the selected calendar.
	TypeGoogleCalendar Type = "GOOGLE_CALENDAR"
)

// ExternalIntegration mirrors external_integrations — the household's own connection to a
// third-party service. RefreshTokenEncrypted/RefreshTokenIV are the ONLY place a secret is ever
// persisted; the plaintext (an OAuth refresh token or a static API key/bot token, depending on
// Type) is never stored or returned once encrypted — see crypto.go and ExternalIntegrationDTO's
// doc comment in dto.go.
type ExternalIntegration struct {
	ID                       uuid.UUID
	Type                     Type
	ConnectedAccount         *string
	RefreshTokenEncrypted    []byte
	RefreshTokenIV           []byte
	Models                   []string
	LastTelegramUpdateID     *int64
	TelegramPairingCode      *string
	TelegramPairingExpiresAt *time.Time
	CreatedAt                time.Time
}

// SystemIntegration mirrors system_integrations — the system-wide connection (historically only
// ever Google Drive, for whole-database backup — see GoogleDriveSistemaIntegracaoService). Since
// that's entirely an OAuth redirect flow, this module only exposes read (Status) and Disconnect —
// there is no Connect for this table (no way to persist a real row without the OAuth exchange
// this module doesn't implement).
type SystemIntegration struct {
	ID                    uuid.UUID
	Type                  Type
	ConnectedAccount      *string
	RefreshTokenEncrypted []byte
	RefreshTokenIV        []byte
	CreatedAt             time.Time
}
