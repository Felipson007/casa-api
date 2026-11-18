package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/curator"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) WasCompleted(ctx context.Context, date time.Time) (bool, error) {
	var completed bool
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ai_curator_runs WHERE run_date = $1)`, date).Scan(&completed)
	})
	return completed, err
}

func (r *Repository) SaveSnapshot(ctx context.Context, date time.Time, insights []curator.Insight, reclassified int) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM ai_insights`); err != nil {
			return fmt.Errorf("curator: clear snapshot: %w", err)
		}
		for _, item := range insights {
			if _, err := tx.Exec(ctx, `INSERT INTO ai_insights (type, severity, title, message) VALUES ($1,$2,$3,$4)`,
				item.Type, item.Severity, item.Title, item.Message); err != nil {
				return fmt.Errorf("curator: insert insight: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ai_curator_runs (run_date, reclassified_count, insight_count)
			VALUES ($1,$2,$3) ON CONFLICT (run_date) DO NOTHING
		`, date, reclassified, len(insights)); err != nil {
			return fmt.Errorf("curator: save run: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("curator: save snapshot: %w", err)
	}
	return nil
}

func (r *Repository) ListInsights(ctx context.Context) ([]curator.Insight, error) {
	result := []curator.Insight{}
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT type, severity, title, message, generated_at FROM ai_insights ORDER BY generated_at DESC, title`)
		if err != nil {
			return fmt.Errorf("curator: list insights: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var item curator.Insight
			if err := rows.Scan(&item.Type, &item.Severity, &item.Title, &item.Message, &item.GeneratedAt); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
