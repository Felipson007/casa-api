// Package postgres implements people.Repository — the only file in the people module that imports
// a SQL driver (docs/architecture-plan.md section 1/2). Same shape as categories/postgres:
// *pgxpool.Pool + db.WithTx, never pool.Query/pool.Begin directly.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ people.Repository = (*Repository)(nil)

const peopleColumns = `id, name, nickname, birth_date, gender, notes, active, email, self_kinship, created_at`

const listPeopleSQL = `SELECT ` + peopleColumns + ` FROM people
	WHERE ($1 = '' OR name ILIKE '%' || $1 || '%' OR COALESCE(nickname, '') ILIKE '%' || $1 || '%'
	       OR COALESCE(email, '') ILIKE '%' || $1 || '%')
	ORDER BY name`

func scanPerson(row interface {
	Scan(dest ...any) error
}) (people.Person, error) {
	var p people.Person
	var birthDate *time.Time
	var gender *string
	var selfKinship *string
	err := row.Scan(&p.ID, &p.Name, &p.Nickname, &birthDate, &gender, &p.Notes, &p.Active, &p.Email, &selfKinship, &p.CreatedAt)
	p.BirthDate = civildate.OfPtr(birthDate)
	if gender != nil {
		p.Gender = people.GenderOf(*gender)
	}
	if selfKinship != nil {
		k := people.LegacyKinship(*selfKinship)
		p.SelfKinship = &k
	}
	return p, err
}

func (r *Repository) ListPeople(ctx context.Context, query string) ([]people.Person, error) {
	result := make([]people.Person, 0)
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, qErr := tx.Query(ctx, listPeopleSQL, query)
		if qErr != nil {
			return fmt.Errorf("querying people: %w", qErr)
		}
		defer rows.Close()
		for rows.Next() {
			p, sErr := scanPerson(rows)
			if sErr != nil {
				return fmt.Errorf("scanning person: %w", sErr)
			}
			result = append(result, p)
		}
		return rows.Err()
	})
	return result, err
}

const findPersonSQL = `SELECT ` + peopleColumns + ` FROM people WHERE id = $1`

func (r *Repository) FindPerson(ctx context.Context, id uuid.UUID) (*people.Person, error) {
	var result *people.Person
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		p, sErr := scanPerson(tx.QueryRow(ctx, findPersonSQL, id))
		if errors.Is(sErr, pgx.ErrNoRows) {
			return nil
		}
		if sErr != nil {
			return fmt.Errorf("scanning person: %w", sErr)
		}
		result = &p
		return nil
	})
	return result, err
}

const insertPersonSQL = `
INSERT INTO people (id, name, nickname, birth_date, gender, notes, active, email, self_kinship, created_at)
VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, now())`

func (r *Repository) CreatePerson(ctx context.Context, p people.Person) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, insertPersonSQL, p.ID, p.Name, p.Nickname, birthDateParam(p.BirthDate), string(p.Gender), p.Notes, p.Active, p.Email, selfKinshipParam(p.SelfKinship)); err != nil {
			return fmt.Errorf("inserting person: %w", err)
		}
		return nil
	})
}

const updatePersonSQL = `
UPDATE people SET name = $2, nickname = $3, birth_date = $4, gender = NULLIF($5, ''), notes = $6, active = $7, email = $8, self_kinship = $9
WHERE id = $1`

func (r *Repository) UpdatePerson(ctx context.Context, p people.Person) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, updatePersonSQL, p.ID, p.Name, p.Nickname, birthDateParam(p.BirthDate), string(p.Gender), p.Notes, p.Active, p.Email, selfKinshipParam(p.SelfKinship))
		if err != nil {
			return fmt.Errorf("updating person: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return people.ErrPersonNotFound
		}
		return nil
	})
}

// birthDateParam converts the domain's *civildate.Date back into the *time.Time pgx has proven,
// native support for binding to a DATE column — civildate.Date only needs to exist at the JSON
// boundary, never at the SQL one.
func birthDateParam(d *civildate.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}

// selfKinshipParam converts the domain's *LegacyKinship into the *string pgx needs — nil stays nil
// (SQL NULL), never the empty string.
func selfKinshipParam(k *people.LegacyKinship) *string {
	if k == nil {
		return nil
	}
	s := string(*k)
	return &s
}

const deletePersonSQL = `DELETE FROM people WHERE id = $1`

func (r *Repository) DeletePerson(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, deletePersonSQL, id)
		if err != nil {
			return fmt.Errorf("deleting person: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return people.ErrPersonNotFound
		}
		return nil
	})
}

// relationshipSelectSQL is the shared base for ListRelationships/FindRelationship — it reads
// through the v_person_relationships view (section 10, item 8) so names come back in one query
// instead of one lookup per edge. Callers append their own ORDER BY/WHERE clause: appending WHERE
// after an ORDER BY here would be invalid SQL, which is why this is factored out rather than
// building findRelationshipSQL directly from listRelationshipsSQL.
const relationshipSelectSQL = `
SELECT r.id, r.from_person_id, p_from.name, r.type, r.to_person_id, p_to.name
FROM person_relationships r
JOIN people p_from ON p_from.id = r.from_person_id
JOIN people p_to   ON p_to.id   = r.to_person_id`

const listRelationshipsSQL = relationshipSelectSQL + ` ORDER BY r.created_at`

func (r *Repository) ListRelationships(ctx context.Context) ([]people.RelationshipDTO, error) {
	result := make([]people.RelationshipDTO, 0)
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, qErr := tx.Query(ctx, listRelationshipsSQL)
		if qErr != nil {
			return fmt.Errorf("querying relationships: %w", qErr)
		}
		defer rows.Close()
		for rows.Next() {
			var rel people.RelationshipDTO
			if sErr := rows.Scan(&rel.ID, &rel.FromPersonID, &rel.FromName, &rel.Type, &rel.ToPersonID, &rel.ToName); sErr != nil {
				return fmt.Errorf("scanning relationship: %w", sErr)
			}
			result = append(result, rel)
		}
		return rows.Err()
	})
	return result, err
}

const findRelationshipSQL = relationshipSelectSQL + ` WHERE r.id = $1`

func (r *Repository) FindRelationship(ctx context.Context, id uuid.UUID) (*people.RelationshipDTO, error) {
	var result *people.RelationshipDTO
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, findRelationshipSQL, id)
		var rel people.RelationshipDTO
		sErr := row.Scan(&rel.ID, &rel.FromPersonID, &rel.FromName, &rel.Type, &rel.ToPersonID, &rel.ToName)
		if errors.Is(sErr, pgx.ErrNoRows) {
			return nil
		}
		if sErr != nil {
			return fmt.Errorf("scanning relationship: %w", sErr)
		}
		result = &rel
		return nil
	})
	return result, err
}

const insertRelationshipSQL = `INSERT INTO person_relationships (id, from_person_id, to_person_id, type, created_at)
VALUES ($1, $2, $3, $4, now())`

func (r *Repository) CreateRelationship(ctx context.Context, rel people.Relationship) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, insertRelationshipSQL, rel.ID, rel.FromPersonID, rel.ToPersonID, string(rel.Type)); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return people.ErrRelationshipExists
			}
			return fmt.Errorf("inserting relationship: %w", err)
		}
		return nil
	})
}

const deleteRelationshipSQL = `DELETE FROM person_relationships WHERE id = $1`

func (r *Repository) DeleteRelationship(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, deleteRelationshipSQL, id); err != nil {
			return fmt.Errorf("deleting relationship: %w", err)
		}
		return nil
	})
}
