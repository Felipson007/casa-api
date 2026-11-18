// Package db provides the connection-pool wiring and the small abstraction that lets a
// repository run against either the real pool or a test transaction without changing its code.
package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is satisfied by both *pgxpool.Pool (production) and pgx.Tx (integration tests), so a
// repository constructor takes a Querier and tests can inject a transaction that gets rolled back
// at teardown — the Go analog of @SpringBootTest's @Transactional per-test rollback.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ Querier = (*pgxpool.Pool)(nil)
	_ Querier = (pgx.Tx)(nil)
)
