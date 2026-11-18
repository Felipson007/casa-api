// Package tenantmigrations holds the domain-only schema applied to each tenant schema
// (`tenant_<slug>`) at provisioning time. It is the baseline produced from the same SQL that built
// the owner's `public` schema, with the control tables (app_users, member_permissions,
// password_reset_tokens, sessions) removed — those live once in the control schema, not per tenant.
//
// This file is embedded so the running server can provision a new tenant schema without shipping
// .sql files separately. See docs/tenant-self-service.md.
package tenantmigrations

import "embed"

//go:embed *.sql
var FS embed.FS
