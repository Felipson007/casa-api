package pandora

import (
	"context"
	"net"
	"net/http"
	"time"
)

// Service proxies OpenAI-compatible chat/vision/tool-calling requests to an AI provider. The
// provider (URL, key, models, auth style) is resolved per call from the context via a
// ConnectorResolver, so each tenant can use their own provider — OmniRoute, OpenAI, Claude, Groq,
// OpenRouter, ... — without any global connection config baked in at boot.
type Service struct {
	resolver   ConnectorResolver
	httpClient *http.Client
}

func NewService(resolver ConnectorResolver) *Service {
	return &Service{
		resolver: resolver,
		httpClient: &http.Client{
			Timeout: defaultChatTimeout,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   20,
				IdleConnTimeout:       60 * time.Second,
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: 3 * time.Minute,
			},
		},
	}
}

// ExtractionModel is the model document extraction should ask for by name for the current tenant,
// falling back to the chat model. Extraction needs a vision-capable, deterministic model (see the
// architecture notes); keeping it separate from the chat model lets a tenant pin it.
func (s *Service) ExtractionModel(ctx context.Context) string {
	c, err := s.resolver.Resolve(ctx)
	if err != nil {
		return ""
	}
	if c.ExtractionModel != "" {
		return c.ExtractionModel
	}
	return c.ChatModel
}

// resolve returns the current connector, or an error if no provider is configured for the context.
func (s *Service) resolve(ctx context.Context) (Connector, error) {
	c, err := s.resolver.Resolve(ctx)
	if err != nil {
		return Connector{}, err
	}
	if c.BaseURL == "" {
		return Connector{}, ErrNoProvider
	}
	return c, nil
}

// applyAuth sets the credential header on req according to the connector's auth style.
func applyAuth(req *http.Request, c Connector) {
	switch c.AuthStyle {
	case AuthXAPIKey:
		req.Header.Set("x-api-key", c.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
}
