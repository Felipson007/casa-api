package aiconnectors

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/pandora"
	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/tenancy"
)

// Resolver resolves the AI connector to use for the current context. It returns the tenant's
// lowest-priority enabled connector (decrypting its key with the tenant's DEK). There is NO
// fallback: a tenant uses only what it configured. A request without tenant context (the public
// dashboard widget) resolves against the default tenant, so it uses the operator's configured
// connector — still a configured connector, never an environment default.
type Resolver struct {
	repo          Repository
	keys          *tenancy.KeyProvider
	defaultTenant tenancy.Tenant
}

func NewResolver(repo Repository, keys *tenancy.KeyProvider, defaultTenant tenancy.Tenant) *Resolver {
	return &Resolver{repo: repo, keys: keys, defaultTenant: defaultTenant}
}

var _ pandora.ConnectorResolver = (*Resolver)(nil)

func (r *Resolver) Resolve(ctx context.Context) (pandora.Connector, error) {
	tenantID, ok := tenancy.TenantIDFromContext(ctx)
	if !ok {
		// No tenant (public widget): use the operator's (default tenant) own connector.
		if r.defaultTenant.ID == uuid.Nil || r.defaultTenant.SchemaName == "" {
			return pandora.Connector{}, nil
		}
		tenantID = r.defaultTenant.ID
		ctx = db.WithTenantSchema(ctx, r.defaultTenant.SchemaName)
	}

	connectors, err := r.repo.List(ctx)
	if err != nil {
		return pandora.Connector{}, err
	}
	var chosen *Connector
	for i := range connectors {
		c := &connectors[i]
		if !c.Enabled {
			continue
		}
		if chosen == nil || c.Priority < chosen.Priority {
			chosen = c
		}
	}
	if chosen == nil {
		// Nothing configured for this tenant: fail (see pandora.Service.resolve), never fall back.
		return pandora.Connector{}, nil
	}
	apiKey, err := r.decryptKey(ctx, tenantID, *chosen)
	if err != nil {
		return pandora.Connector{}, err
	}
	return chosen.toPandora(apiKey), nil
}

func (r *Resolver) decryptKey(ctx context.Context, tenantID uuid.UUID, c Connector) (string, error) {
	if len(c.APIKeyEncrypted) == 0 {
		return "", nil
	}
	cipher, err := r.keys.CipherForTenant(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("aiconnectors: resolving tenant key: %w", err)
	}
	plain, err := cipher.Decrypt(c.APIKeyEncrypted, apiKeyAAD(tenantID, c.ID))
	if err != nil {
		return "", fmt.Errorf("aiconnectors: decrypting api key: %w", err)
	}
	return string(plain), nil
}

// apiKeyAAD binds the encrypted key to its tenant and connector, so a blob cannot be replayed on
// another connector or tenant.
func apiKeyAAD(tenantID, connectorID uuid.UUID) []byte {
	return []byte(tenantID.String() + ":ai_connector:" + connectorID.String() + ":api_key")
}
