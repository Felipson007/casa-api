// Package controlmigrations holds the control-plane schema (app_users, sessions,
// member_permissions, password_reset_tokens, tenants) applied to the shared `public` schema. It is
// tracked in its own `schema_migrations_control` table, independent from each tenant schema's
// migration table. See docs/tenant-self-service.md.
package controlmigrations

import "embed"

//go:embed *.sql
var FS embed.FS
