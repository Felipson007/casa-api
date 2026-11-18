package tenancy

import (
	"context"
	"embed"

	"github.com/atlas/atlas-go/internal/platform/dbmigrate"
)

// SchemaMigratorFrom builds the SchemaMigrator used at provisioning time: it runs the embedded
// tenant migration set against a DSN whose search_path points at the new schema, so every table
// lands in `tenant_<slug>` and the schema keeps its own `schema_migrations` version table.
func SchemaMigratorFrom(fs embed.FS, baseDSN string) SchemaMigrator {
	return func(ctx context.Context, schema string) error {
		dsn, err := DSNWithSearchPath(baseDSN, schema)
		if err != nil {
			return err
		}
		return dbmigrate.Up(fs, dsn)
	}
}
