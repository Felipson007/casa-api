// Package postgres implements clients.Repository — the only file in the clients module that
// imports a SQL driver. Every query goes through db.WithTx, never pool.Query/pool.Begin directly.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/clients"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ clients.Repository = (*Repository)(nil)

func (r *Repository) List(ctx context.Context, activeOnly *bool, query string) ([]clients.Client, error) {
	var result []clients.Client
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, name, phone, email, cpf_cnpj, notes, active, pix_key, created_at
			FROM clients
			WHERE ($1::boolean IS NULL OR active = $1)
			  AND ($2 = '' OR name ILIKE '%' || $2 || '%' OR COALESCE(email, '') ILIKE '%' || $2 || '%'
			       OR COALESCE(phone, '') ILIKE '%' || $2 || '%' OR COALESCE(cpf_cnpj, '') ILIKE '%' || $2 || '%')
			ORDER BY name
		`, activeOnly, query)
		if err != nil {
			return fmt.Errorf("querying clients: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			c, err := scanClient(rows)
			if err != nil {
				return err
			}
			result = append(result, c)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*clients.Client, error) {
	var result *clients.Client
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, name, phone, email, cpf_cnpj, notes, active, pix_key, created_at
			FROM clients
			WHERE id = $1
		`, id)
		c, err := scanClient(row)
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

func (r *Repository) Create(ctx context.Context, c clients.Client) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO clients (id, name, phone, email, cpf_cnpj, notes, active, pix_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, c.ID, c.Name, c.Phone, c.Email, c.CPFCNPJ, c.Notes, c.Active, c.PixKey)
		if err != nil {
			return fmt.Errorf("inserting client: %w", err)
		}
		return nil
	})
}

func (r *Repository) Update(ctx context.Context, c clients.Client) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE clients
			SET name = $2, phone = $3, email = $4, cpf_cnpj = $5, notes = $6, active = $7, pix_key = $8
			WHERE id = $1
		`, c.ID, c.Name, c.Phone, c.Email, c.CPFCNPJ, c.Notes, c.Active, c.PixKey)
		if err != nil {
			return fmt.Errorf("updating client: %w", err)
		}
		return nil
	})
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting client: %w", err)
		}
		return nil
	})
}

// rowScanner is satisfied by both pgx.Rows (List) and pgx.Row (FindByID).
type rowScanner interface {
	Scan(dest ...any) error
}

func scanClient(row rowScanner) (clients.Client, error) {
	var c clients.Client
	err := row.Scan(&c.ID, &c.Name, &c.Phone, &c.Email, &c.CPFCNPJ, &c.Notes, &c.Active, &c.PixKey, &c.CreatedAt)
	return c, err
}
