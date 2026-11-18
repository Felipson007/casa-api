package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/platform/keyring"
	"github.com/atlas/atlas-go/internal/platform/logging"
)

// SchemaMigrator applies the tenant migration set to one schema. Injected so the provisioner does
// not depend on the migration FS/driver (and can be faked in tests).
type SchemaMigrator func(ctx context.Context, schema string) error

// Provisioner creates a new tenant: schema, migrated tables, registry row (with its wrapped DEK)
// and its OWNER user.
type Provisioner struct {
	pool     *pgxpool.Pool
	registry Registry
	migrate  SchemaMigrator
	keyring  *keyring.Keyring
}

func NewProvisioner(pool *pgxpool.Pool, registry Registry, migrate SchemaMigrator, kr *keyring.Keyring) *Provisioner {
	return &Provisioner{pool: pool, registry: registry, migrate: migrate, keyring: kr}
}

// ProvisionRequest is the input of a self-service signup.
type ProvisionRequest struct {
	Slug         string
	DisplayName  string
	Email        string
	Name         string
	PasswordHash string
}

// Provision is the self-service tenant creation. Ordering matters: the schema is created and
// migrated before the registry/user rows are written, so a failure never leaves a registry row
// pointing at a half-built schema; any failure after CREATE SCHEMA drops it again (best-effort).
func (p *Provisioner) Provision(ctx context.Context, req ProvisionRequest) (*Tenant, uuid.UUID, error) {
	slug := NormalizeSlug(req.Slug)
	if !ValidSlug(slug) {
		return nil, uuid.Nil, apperr.Validation("invalid tenant slug",
			"slug must be 3-30 chars, start with a letter, and use only a-z 0-9")
	}
	schema := SchemaNameFor(slug)

	// CREATE SCHEMA fails loudly if the schema already exists (42P06), which is the correct "slug
	// taken" signal; any other failure (e.g. the DB role lacks CREATE on the database) is a real
	// server-side problem and must not be reported as a slug conflict.
	if _, err := p.pool.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P06" {
			return nil, uuid.Nil, apperr.Conflict("tenant slug is not available")
		}
		return nil, uuid.Nil, apperr.Internal(fmt.Errorf("tenancy: creating schema %q: %w", schema, err))
	}

	if err := p.migrate(ctx, schema); err != nil {
		p.dropSchema(ctx, schema)
		return nil, uuid.Nil, apperr.Internal(fmt.Errorf("tenancy: migrating schema %q: %w", schema, err))
	}

	tenant := Tenant{ID: uuid.New(), Slug: slug, SchemaName: schema, DisplayName: req.DisplayName}
	// The tenant is born with its own DEK: generate it now and store only the KEK-wrapped form.
	// Without a configured master key there is no key hierarchy, so refuse rather than store data
	// that could never be crypto-shredded.
	if !p.keyring.Configured() {
		p.dropSchema(ctx, schema)
		return nil, uuid.Nil, apperr.Internal(errors.New("tenancy: TENANT_MASTER_KEY is not configured"))
	}
	dek, err := keyring.GenerateDEK()
	if err != nil {
		p.dropSchema(ctx, schema)
		return nil, uuid.Nil, apperr.Internal(err)
	}
	wrappedDEK, err := p.keyring.Wrap(dek)
	if err != nil {
		p.dropSchema(ctx, schema)
		return nil, uuid.Nil, apperr.Internal(err)
	}

	var userID uuid.UUID
	err = db.WithControlTx(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tenants (id, slug, schema_name, display_name, dek_wrapped)
			VALUES ($1, $2, $3, $4, $5)`, tenant.ID, tenant.Slug, tenant.SchemaName, tenant.DisplayName, wrappedDEK); err != nil {
			return fmt.Errorf("inserting tenant: %w", err)
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO app_users (email, role, password_hash, name, tenant_id)
			VALUES ($1, 'OWNER', $2, $3, $4)
			RETURNING id`, req.Email, req.PasswordHash, req.Name, tenant.ID)
		return row.Scan(&userID)
	})
	if err != nil {
		p.dropSchema(ctx, schema)
		return nil, uuid.Nil, apperr.Internal(fmt.Errorf("tenancy: registering tenant: %w", err))
	}
	return &tenant, userID, nil
}

// dropSchema is best-effort cleanup for a provisioning that failed midway.
func (p *Provisioner) dropSchema(ctx context.Context, schema string) {
	if _, err := p.pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); err != nil {
		logging.FromContext(ctx).Error("tenancy: failed to drop partially-provisioned schema",
			"schema", schema, "cause", err.Error())
	}
}
