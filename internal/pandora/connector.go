package pandora

import "context"

// AuthStyle is how a provider expects the credential to be presented.
type AuthStyle string

const (
	// AuthBearer sends "Authorization: Bearer <key>" — OpenAI, OmniRoute, Groq, OpenRouter,
	// DeepSeek, and Anthropic's OpenAI-compatible endpoint.
	AuthBearer AuthStyle = "BEARER"
	// AuthXAPIKey sends "x-api-key: <key>" plus "anthropic-version" — Anthropic's native API.
	AuthXAPIKey AuthStyle = "X_API_KEY"
)

// Connector is the resolved, ready-to-use configuration for one AI call. It is produced per
// request by a ConnectorResolver (see internal/aiconnectors), so the same Service can talk to a
// different provider/model for each tenant without holding any global connection config.
type Connector struct {
	BaseURL         string
	APIKey          string
	AuthStyle       AuthStyle
	ChatModel       string
	ExtractionModel string
}

// ConnectorResolver resolves the connector to use for the current context. The context carries the
// tenant (schema + id), so a multi-tenant resolver can return that tenant's own provider.
type ConnectorResolver interface {
	Resolve(ctx context.Context) (Connector, error)
}

// staticResolver always returns the same connector — used for the env-var default/fallback and in
// tests.
type staticResolver struct{ c Connector }

// NewStaticResolver wraps a fixed connector as a ConnectorResolver.
func NewStaticResolver(c Connector) ConnectorResolver { return staticResolver{c: c} }

func (s staticResolver) Resolve(context.Context) (Connector, error) { return s.c, nil }
