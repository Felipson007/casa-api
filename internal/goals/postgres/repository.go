// Package postgres implements goals.Repository — the only file in the goals module that imports a
// SQL driver. Every query goes through db.WithTx. Money columns are read/written as string via
// ::text/::numeric casts (same reasoning as finance/loans/expenses). funding_balance_ids is read
// back as text[] and parsed, so the module never depends on a pgx uuid-array codec being
// registered.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/goals"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ goals.Repository = (*Repository)(nil)

const selectGoal = `
	SELECT g.id, g.name, g.kind, g.target_amount::text, g.target_date,
	       g.current_amount::text, g.monthly_contribution::text, g.funding_balance_ids::text[],
	       g.notes, g.status, g.created_at, g.updated_at,
	       COALESCE((SELECT SUM(b.current_value) FROM financial_balances b
	                 WHERE b.id = ANY(g.funding_balance_ids)), 0)::text AS funded_from_balances
	FROM financial_goals g`

func (r *Repository) List(ctx context.Context) ([]goals.Goal, error) {
	var result []goals.Goal
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, selectGoal+`
			ORDER BY (g.status = 'ACTIVE') DESC, g.target_date NULLS LAST, g.name`)
		if err != nil {
			return fmt.Errorf("querying financial_goals: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			g, err := scanGoal(rows)
			if err != nil {
				return err
			}
			result = append(result, g)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*goals.Goal, error) {
	var result *goals.Goal
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		g, err := scanGoal(tx.QueryRow(ctx, selectGoal+` WHERE g.id = $1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &g
		return nil
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, g goals.Goal) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO financial_goals
			  (id, name, kind, target_amount, target_date, current_amount, monthly_contribution,
			   funding_balance_ids, notes, status)
			VALUES ($1, $2, $3, $4::numeric, $5, $6::numeric, $7::numeric, $8::uuid[], $9, $10)
		`, g.ID, g.Name, g.Kind, g.TargetAmount, g.TargetDate, g.CurrentAmount, g.MonthlyContribution,
			idStrings(g.FundingBalanceIDs), g.Notes, g.Status)
		if err != nil {
			return fmt.Errorf("inserting financial_goal: %w", err)
		}
		return nil
	})
}

func (r *Repository) Update(ctx context.Context, g goals.Goal) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE financial_goals
			SET name = $2, kind = $3, target_amount = $4::numeric, target_date = $5,
			    current_amount = $6::numeric, monthly_contribution = $7::numeric,
			    funding_balance_ids = $8::uuid[], notes = $9, status = $10, updated_at = now()
			WHERE id = $1
		`, g.ID, g.Name, g.Kind, g.TargetAmount, g.TargetDate, g.CurrentAmount, g.MonthlyContribution,
			idStrings(g.FundingBalanceIDs), g.Notes, g.Status)
		if err != nil {
			return fmt.Errorf("updating financial_goal: %w", err)
		}
		return nil
	})
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM financial_goals WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting financial_goal: %w", err)
		}
		return nil
	})
}

func (r *Repository) ExistingBalanceIDs(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	var result []uuid.UUID
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text FROM financial_balances WHERE id = ANY($1::uuid[])`, idStrings(ids))
		if err != nil {
			return fmt.Errorf("querying financial_balances by id: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			parsed, perr := uuid.Parse(s)
			if perr != nil {
				return fmt.Errorf("parsing balance id %q: %w", s, perr)
			}
			result = append(result, parsed)
		}
		return rows.Err()
	})
	return result, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanGoal(row rowScanner) (goals.Goal, error) {
	var g goals.Goal
	var fundingIDs []string
	if err := row.Scan(
		&g.ID, &g.Name, &g.Kind, &g.TargetAmount, &g.TargetDate,
		&g.CurrentAmount, &g.MonthlyContribution, &fundingIDs,
		&g.Notes, &g.Status, &g.CreatedAt, &g.UpdatedAt, &g.FundedFromBalances,
	); err != nil {
		return goals.Goal{}, err
	}
	g.FundingBalanceIDs = make([]uuid.UUID, 0, len(fundingIDs))
	for _, s := range fundingIDs {
		parsed, err := uuid.Parse(s)
		if err != nil {
			return goals.Goal{}, fmt.Errorf("parsing funding_balance_id %q: %w", s, err)
		}
		g.FundingBalanceIDs = append(g.FundingBalanceIDs, parsed)
	}
	return g, nil
}

// idStrings passes UUIDs to Postgres as a text[] literal so no pgx uuid-array codec is required —
// the columns/params are cast to uuid[] server-side.
func idStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
