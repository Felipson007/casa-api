package tenancy

import (
	"context"

	"github.com/google/uuid"
)

type tenantIDKey struct{}

// WithTenantID carries the resolved tenant id alongside the schema (db.WithTenantSchema). The
// schema is what scopes SQL; the id is what key-management and per-tenant configuration look up
// (e.g. unwrapping the tenant's DEK, loading its AI connectors).
func WithTenantID(ctx context.Context, id uuid.UUID) context.Context {
	if id == uuid.Nil {
		return ctx
	}
	return context.WithValue(ctx, tenantIDKey{}, id)
}

// TenantIDFromContext returns the tenant id injected by WithTenantID, if any.
func TenantIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(tenantIDKey{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}
