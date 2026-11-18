package db

import (
	"context"
	"testing"
)

func TestTenantSchemaContext(t *testing.T) {
	if _, ok := TenantSchemaFromContext(context.Background()); ok {
		t.Fatal("expected no tenant schema on a bare context")
	}

	ctx := WithTenantSchema(context.Background(), "tenant_acme")
	if got, ok := TenantSchemaFromContext(ctx); !ok || got != "tenant_acme" {
		t.Fatalf("TenantSchemaFromContext = (%q, %v), want (tenant_acme, true)", got, ok)
	}

	// Empty schema must be a no-op so callers can pass an unresolved tenant without accidentally
	// forcing search_path to "" (which would hide public).
	if _, ok := TenantSchemaFromContext(WithTenantSchema(context.Background(), "")); ok {
		t.Fatal("empty schema should not be stored")
	}
}
