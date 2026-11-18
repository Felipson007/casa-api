package db

import "context"

// tenantSchemaKey carries the Postgres schema a request's domain queries must run in. It is set by
// the tenancy middleware (internal/tenancy) after the session resolves the user's tenant, and read
// by WithTx — the single place every domain repository opens a transaction through. Control-plane
// queries (auth, sessions, tenants registry, password reset) deliberately do NOT read it: they call
// WithControlTx, which always runs in the control schema (`public`).
type tenantSchemaKey struct{}

// WithTenantSchema returns a context that makes every subsequent db.WithTx run inside schema. An
// empty schema means "no override" — WithTx falls back to the connection's default search_path
// (i.e. `public`), which is exactly the behavior before tenants existed.
func WithTenantSchema(ctx context.Context, schema string) context.Context {
	if schema == "" {
		return ctx
	}
	return context.WithValue(ctx, tenantSchemaKey{}, schema)
}

// TenantSchemaFromContext returns the schema injected by WithTenantSchema, if any.
func TenantSchemaFromContext(ctx context.Context) (string, bool) {
	schema, ok := ctx.Value(tenantSchemaKey{}).(string)
	return schema, ok && schema != ""
}
