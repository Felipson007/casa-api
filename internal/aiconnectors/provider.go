package aiconnectors

import "github.com/atlas/atlas-go/internal/pandora"

// Provider is a supported AI provider preset. All of them speak the OpenAI-compatible
// /v1/chat/completions protocol; the preset only supplies a default base URL (and, later, an auth
// style) so a tenant can configure a provider with just an API key and a model.
type Provider string

const (
	// ProviderOmniRoute is the operator's own gateway (combo per tenant). Base URL is always
	// tenant-supplied (it points at the operator's instance), so it has no default.
	ProviderOmniRoute Provider = "OMNIROUTE"
	ProviderOpenAI    Provider = "OPENAI"
	// ProviderAnthropic uses Anthropic's OpenAI-compatible endpoint (Bearer auth). A native
	// Messages-API adapter is a possible future addition; not needed for chat/extraction.
	ProviderAnthropic  Provider = "ANTHROPIC"
	ProviderGroq       Provider = "GROQ"
	ProviderOpenRouter Provider = "OPENROUTER"
	ProviderDeepSeek   Provider = "DEEPSEEK"
	ProviderGoogle     Provider = "GOOGLE"
	ProviderCustom     Provider = "CUSTOM"
)

// Valid reports whether p is a known provider preset.
func (p Provider) Valid() bool {
	switch p {
	case ProviderOmniRoute, ProviderOpenAI, ProviderAnthropic, ProviderGroq,
		ProviderOpenRouter, ProviderDeepSeek, ProviderGoogle, ProviderCustom:
		return true
	default:
		return false
	}
}

// defaultBaseURL is the provider's OpenAI-compatible base URL ("" = must be supplied by the tenant).
func (p Provider) defaultBaseURL() string {
	switch p {
	case ProviderOpenAI:
		return "https://api.openai.com/v1"
	case ProviderAnthropic:
		return "https://api.anthropic.com/v1"
	case ProviderGroq:
		return "https://api.groq.com/openai/v1"
	case ProviderOpenRouter:
		return "https://openrouter.ai/api/v1"
	case ProviderDeepSeek:
		return "https://api.deepseek.com/v1"
	case ProviderGoogle:
		return "https://generativelanguage.googleapis.com/v1beta/openai"
	default: // OMNIROUTE, CUSTOM: tenant supplies the URL
		return ""
	}
}

// defaultAuthStyle is how the provider expects the credential.
func (p Provider) defaultAuthStyle() pandora.AuthStyle {
	// All supported presets use Bearer (Anthropic via its OpenAI-compatible endpoint).
	return pandora.AuthBearer
}
