// Package aiconnectors implements per-tenant AI provider configuration ("bring your own key").
// Each tenant stores one or more connectors — provider, base URL, encrypted API key and models —
// and internal/pandora resolves the active one per request. The API key is encrypted with the
// tenant's DEK (internal/platform/keyring), so it is never stored or returned in clear.
package aiconnectors

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/pandora"
)

// Connector is one configured AI provider for a tenant.
type Connector struct {
	ID              uuid.UUID
	Provider        Provider
	Label           string
	BaseURL         string
	AuthStyle       pandora.AuthStyle
	ChatModel       string
	ExtractionModel string
	Priority        int
	Enabled         bool
	// APIKeyEncrypted is the wrapped/encrypted key. It is never serialized to a client — see DTO.
	APIKeyEncrypted []byte
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// HasAPIKey reports whether a key is stored.
func (c Connector) HasAPIKey() bool { return len(c.APIKeyEncrypted) > 0 }

// EffectiveBaseURL is the connector's URL, or the provider preset's default when empty.
func (c Connector) EffectiveBaseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return c.Provider.defaultBaseURL()
}

// toPandora builds the resolved connector passed to the AI client.
func (c Connector) toPandora(apiKey string) pandora.Connector {
	return pandora.Connector{
		BaseURL:         c.EffectiveBaseURL(),
		APIKey:          apiKey,
		AuthStyle:       c.AuthStyle,
		ChatModel:       c.ChatModel,
		ExtractionModel: c.ExtractionModel,
	}
}
