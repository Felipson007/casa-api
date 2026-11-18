// Package tenancy implements the schema-per-tenant model (docs/tenant-self-service.md): a registry
// of tenants, the middleware that resolves a session's tenant schema into the request context, and
// the self-service provisioning that creates a brand-new tenant schema on signup.
//
// Control-plane data (app_users, sessions, tenants, ...) lives in `public`; each tenant's domain
// tables live in `tenant_<slug>`. Nothing here uses a tenant_id column or RLS — isolation is the
// schema namespace plus the centralized search_path applied by db.WithTx.
package tenancy

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SchemaPrefix is prepended to a tenant slug to form its Postgres schema name.
const SchemaPrefix = "tenant_"

// slugPattern keeps slugs to what is safe in both a URL path and a Postgres schema name: lowercase
// letters/digits, starting with a letter, 3–30 chars. The schema name is always derived from the
// slug (never taken raw from user input), so this validation is the single guard on the namespace.
var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9]{2,29}$`)

// Tenant is one isolated dataset. SchemaName is where its domain tables live.
type Tenant struct {
	ID          uuid.UUID
	Slug        string
	SchemaName  string
	DisplayName string
	CreatedAt   time.Time
}

// NormalizeSlug lowercases and trims a proposed slug before validation.
func NormalizeSlug(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidSlug reports whether s (already normalized) is a safe tenant slug.
func ValidSlug(s string) bool {
	return slugPattern.MatchString(s)
}

// SchemaNameFor derives the Postgres schema name for a slug.
func SchemaNameFor(slug string) string {
	return SchemaPrefix + slug
}
