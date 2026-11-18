package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// PluggyCredentials is everything Atlas needs to read the household's Open Finance data from
// Pluggy. It is stored as ONE encrypted JSON blob in external_integrations (type PLUGGY), the same
// AES-GCM path Telegram secrets use — nothing here is ever returned by the API or logged.
type PluggyCredentials struct {
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret"`
	ItemIDs      []string `json:"itemIds"`
}

// ConnectPluggyRequest is the body of PUT /api/openfinance/connection. ClientID/ClientSecret may
// be blank on a reconnect to keep the stored values and only change ItemIDs (same "blank keeps
// the existing secret" rule as Connect).
type ConnectPluggyRequest struct {
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret"`
	ItemIDs      []string `json:"itemIds"`
}

// maskClientID shows just enough of the client id for the UI to say "connected as f468…".
func maskClientID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 4 {
		return id
	}
	return id[:4] + "…"
}

// cleanItemIDs trims, validates (Pluggy item ids are UUIDs) and de-duplicates, preserving order.
func cleanItemIDs(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		// Accept a pasted comma/space/newline separated list inside one entry too.
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }) {
			id, err := uuid.Parse(part)
			if err != nil {
				return nil, apperr.Validation("each item id must be a UUID", "itemIds")
			}
			if s := id.String(); !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// ConnectPluggy stores (or updates) the Pluggy connection.
func (s *Service) ConnectPluggy(ctx context.Context, req ConnectPluggyRequest) (ExternalIntegrationDTO, error) {
	ids, err := cleanItemIDs(req.ItemIDs)
	if err != nil {
		return ExternalIntegrationDTO{}, err
	}
	if len(ids) == 0 {
		return ExternalIntegrationDTO{}, apperr.Validation("at least one item id is required", "itemIds")
	}
	existing, err := s.repo.FindExternalByType(ctx, TypePluggy)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: looking up pluggy connection: %w", err)
	}

	creds := PluggyCredentials{
		ClientID:     strings.TrimSpace(req.ClientID),
		ClientSecret: strings.TrimSpace(req.ClientSecret),
		ItemIDs:      ids,
	}
	e := ExternalIntegration{Type: TypePluggy}
	if existing != nil {
		e.ID = existing.ID
		prev, derr := decodePluggy(existing)
		if derr != nil {
			return ExternalIntegrationDTO{}, derr
		}
		if creds.ClientID == "" {
			creds.ClientID = prev.ClientID
		}
		if creds.ClientSecret == "" {
			creds.ClientSecret = prev.ClientSecret
		}
	} else {
		e.ID = uuid.New()
	}
	if creds.ClientID == "" || creds.ClientSecret == "" {
		return ExternalIntegrationDTO{}, apperr.Validation("clientId and clientSecret are required to connect for the first time", "clientSecret")
	}

	payload, err := json.Marshal(creds)
	if err != nil {
		return ExternalIntegrationDTO{}, apperr.Internal(err)
	}
	encrypted, err := Encrypt(string(payload))
	if err != nil {
		return ExternalIntegrationDTO{}, apperr.Internal(err)
	}
	e.RefreshTokenEncrypted, e.RefreshTokenIV = encrypted.Ciphertext, encrypted.IV
	account := maskClientID(creds.ClientID)
	e.ConnectedAccount = &account

	if err := s.repo.UpsertExternal(ctx, e); err != nil {
		return ExternalIntegrationDTO{}, err
	}
	persisted, err := s.repo.FindExternalByType(ctx, TypePluggy)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: re-reading after pluggy connect: %w", err)
	}
	return toExternalDTO(*persisted), nil
}

// PluggyCredentials returns the decrypted credentials, or ok=false when Pluggy isn't connected.
// Used by the background sync only; never exposed over HTTP.
func (s *Service) PluggyCredentials(ctx context.Context) (creds PluggyCredentials, ok bool, err error) {
	existing, err := s.repo.FindExternalByType(ctx, TypePluggy)
	if err != nil {
		return PluggyCredentials{}, false, fmt.Errorf("integrations: looking up pluggy connection: %w", err)
	}
	if existing == nil {
		return PluggyCredentials{}, false, nil
	}
	creds, err = decodePluggy(existing)
	if err != nil {
		return PluggyCredentials{}, false, err
	}
	return creds, true, nil
}

func decodePluggy(e *ExternalIntegration) (PluggyCredentials, error) {
	plain, err := Decrypt(e.RefreshTokenEncrypted, e.RefreshTokenIV)
	if err != nil {
		return PluggyCredentials{}, fmt.Errorf("integrations: decrypting pluggy credentials: %w", err)
	}
	var creds PluggyCredentials
	if err := json.Unmarshal([]byte(plain), &creds); err != nil {
		return PluggyCredentials{}, fmt.Errorf("integrations: stored pluggy credentials are malformed: %w", err)
	}
	return creds, nil
}
