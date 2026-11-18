package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/platform/logging"
)

// WithTx is the one sanctioned way to run a domain query inside a transaction: it opens the
// transaction, runs fn, and commits or rolls back — no repository calls pool.Begin/pool.Query
// directly. When the context carries a tenant schema (db.WithTenantSchema, set by
// internal/tenancy's middleware from the session's user), the transaction sets
// `search_path` to that schema for its duration, so every unqualified table name in the domain
// resolves inside the tenant's own schema. No schema in context (public routes, control queries,
// tests) means the connection's default search_path (`public`) is used, unchanged.
//
// This is the schema-per-tenant analog of the per-tenant `SET LOCAL app.tenant_id` this helper
// used to issue for Row-Level Security before the app was declared single-tenant
// (docs/architecture-plan.md section 3); the centralization is what makes both safe.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	schema, _ := TenantSchemaFromContext(ctx)
	return runTx(ctx, pool, schema, fn)
}

// WithControlTx runs fn in the control schema regardless of any tenant schema in the context. It is
// for control-plane tables (app_users, sessions, member_permissions, password_reset_tokens,
// tenants), which always live in `public` and must never be resolved against a tenant's
// search_path. Mirrors WithTx otherwise.
func WithControlTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	return runTx(ctx, pool, "", fn)
}

// runTx holds the shared transaction lifecycle. schema == "" means "leave the connection's
// search_path alone". set_config(..., true) is transaction-scoped, so it cannot leak to another
// request that later checks out the same pooled connection; passing the schema as a bind parameter
// (rather than interpolating it into SQL) keeps it injection-safe even if a caller ever passes an
// unexpected value.
func runTx(ctx context.Context, pool *pgxpool.Pool, schema string, fn func(tx pgx.Tx) error) error {
	acquireStarted := time.Now()
	tx, err := pool.Begin(ctx)
	acquireDuration := time.Since(acquireStarted)
	if acquireDuration >= 10*time.Millisecond {
		stats := pool.Stat()
		logging.FromContext(ctx).Warn("db pool acquire slow",
			"acquire_ms", acquireDuration.Milliseconds(),
			"acquired_conns", stats.AcquiredConns(),
			"total_conns", stats.TotalConns(),
			"idle_conns", stats.IdleConns(),
			"empty_acquires", stats.EmptyAcquireCount(),
		)
	}
	if err != nil {
		return fmt.Errorf("db: beginning tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if already committed

	if schema != "" {
		if _, err := tx.Exec(ctx, `SELECT set_config('search_path', $1, true)`, schema); err != nil {
			return fmt.Errorf("db: setting tenant search_path to %q: %w", schema, err)
		}
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: committing tx: %w", err)
	}
	return nil
}
