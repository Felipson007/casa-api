package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool opens the pgx connection pool for the given DSN. Callers pass DATABASE_URL in
// production and TEST_DATABASE_URL in tests — the two are always distinct env vars, mirroring
// the existing DB_URL/TEST_DB_URL separation, so a missing flag can never point a test run at the
// real tenant data.
func NewPool(ctx context.Context, dsn string, statementTimeout time.Duration) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parsing pool config: %w", err)
	}
	if statementTimeout > 0 {
		if poolConfig.ConnConfig.RuntimeParams == nil {
			poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
		}
		poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = statementTimeout.Round(time.Millisecond).String()
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("db: opening pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping failed: %w", err)
	}
	return pool, nil
}
