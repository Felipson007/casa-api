// Package postgres implements categories.Repository — the only file in the categories module that
// imports a SQL driver (docs/architecture-plan.md section 1/2). Every query goes through
// db.WithTx, never pool.Query/pool.Begin directly.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/categories"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ categories.Repository = (*Repository)(nil)

func (r *Repository) List(ctx context.Context, filterType *categories.Type) ([]categories.Category, error) {
	var result []categories.Category
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, name, parent_category_id, type, created_at
			FROM categories
			WHERE ($1::text IS NULL OR type = $1)
			ORDER BY name
		`, filterType)
		if err != nil {
			return fmt.Errorf("querying categories: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			c, err := scanCategory(rows)
			if err != nil {
				return err
			}
			result = append(result, c)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*categories.Category, error) {
	var result *categories.Category
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, name, parent_category_id, type, created_at
			FROM categories
			WHERE id = $1
		`, id)
		c, err := scanCategory(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &c
		return nil
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, c categories.Category) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO categories (id, name, parent_category_id, type)
			VALUES ($1, $2, $3, $4)
		`, c.ID, c.Name, c.ParentCategoryID, c.Type)
		if err != nil {
			return fmt.Errorf("inserting category: %w", err)
		}
		return nil
	})
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM categories WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting category: %w", err)
		}
		return nil
	})
}

func (r *Repository) CountAll(ctx context.Context) (int, error) {
	var count int
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM categories`).Scan(&count)
	})
	return count, err
}

// rowScanner is satisfied by both pgx.Rows (List) and pgx.Row (FindByID), so scanCategory has one
// implementation instead of two near-identical copies.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanCategory(row rowScanner) (categories.Category, error) {
	var c categories.Category
	var categoryType *categories.Type
	err := row.Scan(&c.ID, &c.Name, &c.ParentCategoryID, &categoryType, &c.CreatedAt)
	c.Type = categoryType
	return c, err
}
