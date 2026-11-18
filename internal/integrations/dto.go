package integrations

import (
	"time"

	"github.com/google/uuid"
)

// ExternalIntegrationDTO is the JSON shape returned to clients. Never carries the secret — only
// enough for the UI to show "connected as fulano@gmail.com" (or, for API-key-only types with no
// account concept, just that it's connected) — mirrors the old Java IntegracaoExternaDto exactly.
type ExternalIntegrationDTO struct {
	ID               uuid.UUID `json:"id"`
	Type             Type      `json:"type"`
	ConnectedAccount *string   `json:"connectedAccount,omitempty"`
	Models           []string  `json:"models,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

// ConnectExternalIntegrationRequest is the JSON body accepted by
// POST /api/integrations/external/connect. Type excludes GOOGLE_DRIVE — the only OAuth-based
// type, which this module can't connect without the real OAuth exchange (see package doc). Secret
// is the plaintext API key/bot token — required the first time a Type is connected, may be omitted
// on a later reconnect to keep the currently-stored secret and only change
// Models/ConnectedAccount (mirrors GeminiIntegracaoService.conectar's exact "blank keeps the
// existing key" behavior).
type ConnectExternalIntegrationRequest struct {
	Type             Type     `json:"type" validate:"required,oneof=TELEGRAM"`
	ConnectedAccount *string  `json:"connectedAccount"`
	Secret           *string  `json:"secret"`
	Models           []string `json:"models"`
}

// SystemIntegrationDTO is the JSON shape returned by GET /api/integrations/system. Never carries
// the secret.
type SystemIntegrationDTO struct {
	ID               uuid.UUID `json:"id"`
	Type             Type      `json:"type"`
	ConnectedAccount *string   `json:"connectedAccount,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}
