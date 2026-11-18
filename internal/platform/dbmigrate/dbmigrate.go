// Package dbmigrate applies internal/migrations against DATABASE_URL on cmd/api boot — no
// manual `psql`/SSH step is needed for a deploy to reach the right schema, and no separate
// migration binary/CLI needs to be invoked out of band: the running server brings the schema up
// to date itself, before it starts accepting requests.
package dbmigrate

import (
	"embed"
	"errors"
	"fmt"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Up applies every pending migration in fs (internal/migrations.FS) against dsn. Returns nil both
// when migrations were actually applied and when the schema was already at the latest version
// (migrate.ErrNoChange) — callers never need to special-case "nothing to do" themselves.
func Up(fs embed.FS, dsn string) error {
	source, err := iofs.New(fs, ".")
	if err != nil {
		return fmt.Errorf("dbmigrate: loading embedded migrations: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, dsn)
	if err != nil {
		return fmt.Errorf("dbmigrate: initializing migrator: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("dbmigrate: applying migrations: %w", err)
	}
	return nil
}

// UpTable is Up with an explicit golang-migrate version table name, so independent migration
// streams can target the same database without colliding (e.g. controlmigrations tracked in
// `schema_migrations_control` while tenant schemas keep their own `schema_migrations`).
func UpTable(fs embed.FS, dsn, migrationsTable string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("dbmigrate: parsing database URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("dbmigrate: database URL is not in URL form (postgres://...)")
	}
	q := u.Query()
	q.Set("x-migrations-table", migrationsTable)
	u.RawQuery = q.Encode()
	return Up(fs, u.String())
}
