package aiconnectors

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/tenancy"
)

// Service is the CRUD surface for a tenant's connectors, handling encryption of the API key with
// the tenant's DEK.
type Service struct {
	repo Repository
	keys *tenancy.KeyProvider
}

func NewService(repo Repository, keys *tenancy.KeyProvider) *Service {
	return &Service{repo: repo, keys: keys}
}

// UpsertInput is the writable shape of a connector. APIKey is plaintext on input only; empty means
// "keep the stored key" on update.
type UpsertInput struct {
	Provider        Provider
	Label           string
	BaseURL         string
	APIKey          string
	ChatModel       string
	ExtractionModel string
	Priority        int
	Enabled         bool
}

func (s *Service) List(ctx context.Context) ([]Connector, error) {
	return s.repo.List(ctx)
}

func (s *Service) Create(ctx context.Context, in UpsertInput) (*Connector, error) {
	if !in.Provider.Valid() {
		return nil, apperr.Validation("invalid AI provider")
	}
	tenantID, ok := tenancy.TenantIDFromContext(ctx)
	if !ok {
		return nil, apperr.Unauthorized("no tenant in context")
	}
	id := uuid.New()
	enc, err := s.encrypt(ctx, tenantID, id, in.APIKey)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	c := Connector{
		ID:              id,
		Provider:        in.Provider,
		Label:           in.Label,
		BaseURL:         in.BaseURL,
		AuthStyle:       in.Provider.defaultAuthStyle(),
		ChatModel:       in.ChatModel,
		ExtractionModel: in.ExtractionModel,
		Priority:        priorityOr(in.Priority),
		Enabled:         in.Enabled,
		APIKeyEncrypted: enc,
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return nil, apperr.Internal(err)
	}
	return &c, nil
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpsertInput) (*Connector, error) {
	if !in.Provider.Valid() {
		return nil, apperr.Validation("invalid AI provider")
	}
	existing, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if existing == nil {
		return nil, apperr.NotFound("connector not found")
	}
	existing.Provider = in.Provider
	existing.Label = in.Label
	existing.BaseURL = in.BaseURL
	existing.AuthStyle = in.Provider.defaultAuthStyle()
	existing.ChatModel = in.ChatModel
	existing.ExtractionModel = in.ExtractionModel
	existing.Priority = priorityOr(in.Priority)
	existing.Enabled = in.Enabled
	if in.APIKey != "" {
		tenantID, _ := tenancy.TenantIDFromContext(ctx)
		enc, err := s.encrypt(ctx, tenantID, id, in.APIKey)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		existing.APIKeyEncrypted = enc
	}
	if err := s.repo.Update(ctx, *existing); err != nil {
		return nil, apperr.Internal(err)
	}
	return existing, nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

func (s *Service) encrypt(ctx context.Context, tenantID, connectorID uuid.UUID, apiKey string) ([]byte, error) {
	if apiKey == "" {
		return nil, nil
	}
	if !s.keys.Configured() {
		return nil, fmt.Errorf("aiconnectors: TENANT_MASTER_KEY not configured")
	}
	cipher, err := s.keys.CipherForTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return cipher.Encrypt([]byte(apiKey), apiKeyAAD(tenantID, connectorID))
}

func priorityOr(p int) int {
	if p == 0 {
		return 100
	}
	return p
}
