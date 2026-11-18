package tenancy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

func TestValidSlug(t *testing.T) {
	cases := map[string]bool{
		"acme":      true,
		"acme_corp": false, // underscore not allowed by slugPattern
		"a":         false, // too short
		"ab":        false, // too short (min 3)
		"abc":       true,
		"1abc":      false, // must start with a letter
		"Acme":      false, // must be normalized first
		"acme-corp": false, // hyphen not allowed
	}
	for in, want := range cases {
		if got := ValidSlug(in); got != want {
			t.Errorf("ValidSlug(%q) = %v, want %v", in, got, want)
		}
	}
	if !ValidSlug(strings.Repeat("a", 30)) {
		t.Error("30-char slug should be valid")
	}
	if ValidSlug(strings.Repeat("a", 31)) {
		t.Error("31-char slug should be invalid")
	}
}

func TestNormalizeSlug(t *testing.T) {
	if got := NormalizeSlug("  Acme  "); got != "acme" {
		t.Fatalf("NormalizeSlug = %q, want %q", got, "acme")
	}
}

func TestSchemaNameFor(t *testing.T) {
	if got := SchemaNameFor("acme"); got != "tenant_acme" {
		t.Fatalf("SchemaNameFor = %q, want tenant_acme", got)
	}
}

func TestDSNWithSearchPath(t *testing.T) {
	base := "postgres://user:pass@localhost:5432/atlas?sslmode=disable"
	got, err := DSNWithSearchPath(base, "tenant_acme")
	if err != nil {
		t.Fatalf("DSNWithSearchPath: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("result is not a valid URL: %v", err)
	}
	if sp := u.Query().Get("search_path"); sp != "tenant_acme" {
		t.Fatalf("search_path = %q, want tenant_acme", sp)
	}
	if u.Query().Get("sslmode") != "disable" {
		t.Fatalf("existing query params were dropped: %q", got)
	}
}

func TestDSNWithSearchPathRejectsInvalidSchema(t *testing.T) {
	if _, err := DSNWithSearchPath("postgres://u:p@h/db", "public"); err == nil {
		t.Fatal("expected error for schema without tenant_ prefix")
	}
	if _, err := DSNWithSearchPath("postgres://u:p@h/db", "tenant_BAD"); err == nil {
		t.Fatal("expected error for invalid slug")
	}
	if _, err := DSNWithSearchPath("host=localhost dbname=x", "tenant_acme"); err == nil {
		t.Fatal("expected error for non-URL DSN")
	}
}

func TestWithTenantSchemaInjectsContext(t *testing.T) {
	var got string
	var had bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, had = db.TenantSchemaFromContext(r.Context())
	})
	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	req = req.WithContext(httpx.WithUser(req.Context(), httpx.User{ID: uuid.New(), TenantSchema: "tenant_acme"}))
	WithTenantSchema(next).ServeHTTP(httptest.NewRecorder(), req)
	if !had || got != "tenant_acme" {
		t.Fatalf("tenant schema in context = (%q, %v), want (tenant_acme, true)", got, had)
	}
}

func TestWithTenantSchemaIgnoresEmpty(t *testing.T) {
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if s, ok := db.TenantSchemaFromContext(r.Context()); ok {
			t.Fatalf("unexpected schema %q in context", s)
		}
	})
	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	req = req.WithContext(httpx.WithUser(req.Context(), httpx.User{ID: uuid.New()}))
	WithTenantSchema(next).ServeHTTP(httptest.NewRecorder(), req)
}

func TestProvisionRejectsInvalidSlug(t *testing.T) {
	migrated := false
	p := NewProvisioner(nil, nil, func(context.Context, string) error {
		migrated = true
		return nil
	}, nil)
	if _, _, err := p.Provision(context.Background(), ProvisionRequest{Slug: "Bad Slug!"}); err == nil {
		t.Fatal("expected validation error for invalid slug")
	}
	if migrated {
		t.Fatal("migrator must not run for an invalid slug")
	}
}
