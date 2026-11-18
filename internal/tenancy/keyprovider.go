package tenancy

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/keyring"
)

// KeyProvider resolves a tenant's data-encryption key from the registry (where it is stored
// wrapped by the master KEK) and caches the derived Cipher. It is the single place modules ask for
// "the key that encrypts this tenant's data".
type KeyProvider struct {
	registry Registry
	keyring  *keyring.Keyring

	mu    sync.RWMutex
	cache map[uuid.UUID]*keyring.Cipher
}

func NewKeyProvider(registry Registry, kr *keyring.Keyring) *KeyProvider {
	return &KeyProvider{registry: registry, keyring: kr, cache: make(map[uuid.UUID]*keyring.Cipher)}
}

// Configured reports whether a master key is available for wrap/unwrap.
func (p *KeyProvider) Configured() bool { return p.keyring.Configured() }

// CipherForContext returns the cipher for the tenant in the context, or (nil, nil) when the
// context carries no tenant (public routes, background tasks without a tenant). Callers treat a
// nil cipher as "encryption not applicable here".
func (p *KeyProvider) CipherForContext(ctx context.Context) (*keyring.Cipher, error) {
	id, ok := TenantIDFromContext(ctx)
	if !ok {
		return nil, nil
	}
	return p.CipherForTenant(ctx, id)
}

// EnsureTenantKey creates and stores a wrapped DEK for tenantID when it has none. Idempotent; used
// at boot for the default (owner) tenant and after provisioning.
func (p *KeyProvider) EnsureTenantKey(ctx context.Context, tenantID uuid.UUID) error {
	if !p.keyring.Configured() {
		return errors.New("tenancy: TENANT_MASTER_KEY is not configured — cannot create a tenant key")
	}
	wrapped, err := p.registry.DEK(ctx, tenantID)
	if err != nil {
		return err
	}
	if len(wrapped) > 0 {
		return nil
	}
	dek, err := keyring.GenerateDEK()
	if err != nil {
		return err
	}
	wrapped, err = p.keyring.Wrap(dek)
	if err != nil {
		return err
	}
	return p.registry.SetDEK(ctx, tenantID, wrapped)
}

// CipherForTenant returns the tenant's Cipher, unwrapping the DEK on first use.
func (p *KeyProvider) CipherForTenant(ctx context.Context, tenantID uuid.UUID) (*keyring.Cipher, error) {
	if !p.keyring.Configured() {
		return nil, errors.New("tenancy: TENANT_MASTER_KEY is not configured")
	}
	p.mu.RLock()
	c, ok := p.cache[tenantID]
	p.mu.RUnlock()
	if ok {
		return c, nil
	}
	wrapped, err := p.registry.DEK(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if len(wrapped) == 0 {
		return nil, fmt.Errorf("tenancy: tenant %s has no DEK provisioned", tenantID)
	}
	dek, err := p.keyring.Unwrap(wrapped)
	if err != nil {
		return nil, err
	}
	c, err = keyring.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.cache[tenantID] = c
	p.mu.Unlock()
	return c, nil
}
