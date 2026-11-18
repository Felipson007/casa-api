package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/platform/db"
)

// Registry is the control-plane store of tenants. Implemented by PostgresRegistry; defined as an
// interface so the provisioner/handler can be tested with a fake.
type Registry interface {
	Create(ctx context.Context, t Tenant) error
	ByID(ctx context.Context, id uuid.UUID) (*Tenant, error)
	BySlug(ctx context.Context, slug string) (*Tenant, error)
	// List returns every registered tenant (used at boot to migrate each tenant schema, and by the
	// worker to run per-tenant tasks).
	List(ctx context.Context) ([]Tenant, error)
	// EnsureDefault upserts the tenant that owns the current database's existing `public` schema
	// (the owner, pre-cutover). Idempotent — safe to call on every boot.
	EnsureDefault(ctx context.Context, slug, schemaName, displayName string) (*Tenant, error)
	// AssignUsersWithoutTenant backfills app_users.tenant_id for legacy rows (the owner's users),
	// returning how many rows were touched.
	AssignUsersWithoutTenant(ctx context.Context, tenantID uuid.UUID) (int64, error)
	// DEK returns the tenant's wrapped data-encryption key (nil/empty when not yet provisioned).
	DEK(ctx context.Context, id uuid.UUID) ([]byte, error)
	// SetDEK stores the tenant's wrapped DEK.
	SetDEK(ctx context.Context, id uuid.UUID, wrapped []byte) error
}

// PostgresRegistry is the control-pool-backed Registry.
type PostgresRegistry struct {
	pool *pgxpool.Pool
}

func NewRegistry(pool *pgxpool.Pool) *PostgresRegistry {
	return &PostgresRegistry{pool: pool}
}

var _ Registry = (*PostgresRegistry)(nil)

func (r *PostgresRegistry) Create(ctx context.Context, t Tenant) error {
	return db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenants (id, slug, schema_name, display_name)
			VALUES ($1, $2, $3, $4)`,
			t.ID, t.Slug, t.SchemaName, t.DisplayName)
		if err != nil {
			return fmt.Errorf("tenancy: inserting tenant: %w", err)
		}
		return nil
	})
}

func (r *PostgresRegistry) ByID(ctx context.Context, id uuid.UUID) (*Tenant, error) {
	return r.scanOne(ctx, `WHERE id = $1`, id)
}

func (r *PostgresRegistry) BySlug(ctx context.Context, slug string) (*Tenant, error) {
	return r.scanOne(ctx, `WHERE slug = $1`, slug)
}

func (r *PostgresRegistry) List(ctx context.Context) ([]Tenant, error) {
	var out []Tenant
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, slug, schema_name, COALESCE(display_name, ''), created_at
			FROM tenants ORDER BY created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t Tenant
			if err := rows.Scan(&t.ID, &t.Slug, &t.SchemaName, &t.DisplayName, &t.CreatedAt); err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("tenancy: listing tenants: %w", err)
	}
	return out, nil
}

func (r *PostgresRegistry) scanOne(ctx context.Context, where string, arg any) (*Tenant, error) {
	var t Tenant
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, slug, schema_name, COALESCE(display_name, ''), created_at
			FROM tenants `+where, arg)
		return row.Scan(&t.ID, &t.Slug, &t.SchemaName, &t.DisplayName, &t.CreatedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("tenancy: loading tenant: %w", err)
	}
	return &t, nil
}

func (r *PostgresRegistry) EnsureDefault(ctx context.Context, slug, schemaName, displayName string) (*Tenant, error) {
	var t Tenant
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO tenants (slug, schema_name, display_name)
			VALUES ($1, $2, $3)
			ON CONFLICT (slug) DO UPDATE SET schema_name = EXCLUDED.schema_name,
			                                 display_name = EXCLUDED.display_name
			RETURNING id, slug, schema_name, COALESCE(display_name, ''), created_at`,
			slug, schemaName, displayName)
		return row.Scan(&t.ID, &t.Slug, &t.SchemaName, &t.DisplayName, &t.CreatedAt)
	})
	if err != nil {
		return nil, fmt.Errorf("tenancy: ensuring default tenant: %w", err)
	}
	return &t, nil
}

func (r *PostgresRegistry) DEK(ctx context.Context, id uuid.UUID) ([]byte, error) {
	var wrapped []byte
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT dek_wrapped FROM tenants WHERE id = $1`, id).Scan(&wrapped)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("tenancy: loading tenant DEK: %w", err)
	}
	return wrapped, nil
}

func (r *PostgresRegistry) SetDEK(ctx context.Context, id uuid.UUID, wrapped []byte) error {
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE tenants SET dek_wrapped = $2 WHERE id = $1`, id, wrapped)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("tenancy: storing tenant DEK: %w", err)
	}
	return nil
}

func (r *PostgresRegistry) AssignUsersWithoutTenant(ctx context.Context, tenantID uuid.UUID) (int64, error) {
	var n int64
	err := db.WithControlTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app_users SET tenant_id = $1 WHERE tenant_id IS NULL`, tenantID)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("tenancy: backfilling app_users.tenant_id: %w", err)
	}
	return n, nil
}
