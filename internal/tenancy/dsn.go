package tenancy

import (
	"fmt"
	"net/url"
)

// DSNWithSearchPath returns baseDSN with `search_path=<schema>` set, so a tool connecting with it
// resolves unqualified names inside that schema. Used for the per-schema golang-migrate run: with
// search_path set, `CURRENT_SCHEMA()` is the tenant schema and the driver creates/tracks
// `schema_migrations` inside it (see golang-migrate's postgres driver). The base DSN must be a
// URL-form Postgres DSN (postgres://...), which is what DATABASE_URL already is.
func DSNWithSearchPath(baseDSN, schema string) (string, error) {
	if len(schema) <= len(SchemaPrefix) || schema[:len(SchemaPrefix)] != SchemaPrefix || !ValidSlug(schema[len(SchemaPrefix):]) {
		return "", fmt.Errorf("tenancy: refusing to build DSN for invalid schema %q", schema)
	}
	u, err := url.Parse(baseDSN)
	if err != nil {
		return "", fmt.Errorf("tenancy: parsing database URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("tenancy: database URL is not in URL form (postgres://...)")
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
