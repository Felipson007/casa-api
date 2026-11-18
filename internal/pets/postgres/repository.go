// Package postgres implements pets.Repository — the only file in the pets module that imports a
// SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/pets"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ pets.Repository = (*Repository)(nil)

func (r *Repository) List(ctx context.Context, activeOnly *bool) ([]pets.Pet, error) {
	var result []pets.Pet
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, name, species, breed, birth_date, notes, active, created_at
			FROM pets
			WHERE ($1::boolean IS NULL OR active = $1)
			ORDER BY name
		`, activeOnly)
		if err != nil {
			return fmt.Errorf("querying pets: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			p, err := scanPet(rows)
			if err != nil {
				return err
			}
			result = append(result, p)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*pets.Pet, error) {
	var result *pets.Pet
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, name, species, breed, birth_date, notes, active, created_at
			FROM pets
			WHERE id = $1
		`, id)
		p, err := scanPet(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &p
		return nil
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, p pets.Pet) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO pets (id, name, species, breed, birth_date, notes, active)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, p.ID, p.Name, p.Species, p.Breed, p.BirthDate, p.Notes, p.Active)
		if err != nil {
			return fmt.Errorf("inserting pet: %w", err)
		}
		return nil
	})
}

func (r *Repository) Update(ctx context.Context, p pets.Pet) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE pets
			SET name = $2, species = $3, breed = $4, birth_date = $5, notes = $6, active = $7
			WHERE id = $1
		`, p.ID, p.Name, p.Species, p.Breed, p.BirthDate, p.Notes, p.Active)
		if err != nil {
			return fmt.Errorf("updating pet: %w", err)
		}
		return nil
	})
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM pets WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting pet: %w", err)
		}
		return nil
	})
}

const recordColumns = `id, pet_id, type, name, record_date, status, amount::text, notes, created_at`

func (r *Repository) CreateRecord(ctx context.Context, rec pets.Record) (pets.Record, error) {
	var created pets.Record
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO pet_records (id, pet_id, type, name, record_date, status, amount, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7::numeric, $8)
			RETURNING `+recordColumns, rec.ID, rec.PetID, rec.Type, rec.Name, rec.RecordDate, string(rec.Status), rec.Amount, rec.Notes)
		var err error
		created, err = scanRecord(row)
		return err
	})
	if err != nil {
		return pets.Record{}, fmt.Errorf("inserting pet record: %w", err)
	}
	return created, nil
}

func (r *Repository) ListRecordsByPet(ctx context.Context, petID uuid.UUID) ([]pets.Record, error) {
	var result []pets.Record
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+recordColumns+` FROM pet_records WHERE pet_id = $1 ORDER BY record_date DESC`, petID)
		if err != nil {
			return fmt.Errorf("querying pet records: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			rec, err := scanRecord(rows)
			if err != nil {
				return err
			}
			result = append(result, rec)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) ListUpcomingVaccines(ctx context.Context, petID uuid.UUID, today time.Time) ([]pets.Record, error) {
	var result []pets.Record
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT `+recordColumns+` FROM pet_records
			WHERE pet_id = $1 AND type = 'VACCINE' AND status = 'PENDING' AND record_date >= $2
			ORDER BY record_date ASC
		`, petID, today)
		if err != nil {
			return fmt.Errorf("querying upcoming vaccines: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			rec, err := scanRecord(rows)
			if err != nil {
				return err
			}
			result = append(result, rec)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindRecordByID(ctx context.Context, id uuid.UUID) (*pets.Record, error) {
	var result *pets.Record
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+recordColumns+` FROM pet_records WHERE id = $1`, id)
		rec, err := scanRecord(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &rec
		return nil
	})
	return result, err
}

func (r *Repository) DeleteRecord(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM pet_records WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting pet record: %w", err)
		}
		return nil
	})
}

func scanRecord(row rowScanner) (pets.Record, error) {
	var rec pets.Record
	err := row.Scan(&rec.ID, &rec.PetID, &rec.Type, &rec.Name, &rec.RecordDate, &rec.Status, &rec.Amount, &rec.Notes, &rec.CreatedAt)
	return rec, err
}

// rowScanner is satisfied by both pgx.Rows and pgx.Row.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPet(row rowScanner) (pets.Pet, error) {
	var p pets.Pet
	err := row.Scan(&p.ID, &p.Name, &p.Species, &p.Breed, &p.BirthDate, &p.Notes, &p.Active, &p.CreatedAt)
	return p, err
}
