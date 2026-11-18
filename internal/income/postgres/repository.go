// Package postgres implements income.Repository — the only file in the income module that
// imports a SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/categorize"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ income.Repository = (*Repository)(nil)

// categorySourceOrNone keeps category_source inside its CHECK constraint
// (NONE/AI/RULE/HISTORY/MANUAL): callers that build an Entry directly may leave it empty, which
// the column rejects. The service normalises for the HTTP path, but the repository is the last line
// of defence.
func categorySourceOrNone(s string) string {
	if s == "" {
		return income.CategorySourceNone
	}
	return s
}

func (r *Repository) List(ctx context.Context, categoryID, personID *uuid.UUID, query string) ([]income.Entry, error) {
	var result []income.Entry
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, document_id, category_id, person_id, amount::text, income_date, original_date,
			       description, reviewed, created_at, usd_amount::text, exchange_rate::text, source,
			       category_source
			FROM income_entries
			WHERE ($1::uuid IS NULL OR category_id = $1)
			  AND ($2::uuid IS NULL OR person_id = $2)
			  AND ($3 = '' OR COALESCE(description, '') ILIKE '%' || $3 || '%')
			ORDER BY income_date DESC
		`, categoryID, personID, query)
		if err != nil {
			return fmt.Errorf("querying income entries: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			e, err := scanEntry(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*income.Entry, error) {
	var result *income.Entry
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, document_id, category_id, person_id, amount::text, income_date, original_date,
			       description, reviewed, created_at, usd_amount::text, exchange_rate::text, source,
			       category_source
			FROM income_entries
			WHERE id = $1
		`, id)
		e, err := scanEntry(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &e
		return nil
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, e income.Entry) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO income_entries (id, document_id, category_id, person_id, amount, income_date,
			                             original_date, description, reviewed, usd_amount, exchange_rate,
			                             category_source)
			VALUES ($1, $2, $3, $4, $5::numeric, $6, $7, $8, $9, $10::numeric, $11::numeric, $12)
		`, e.ID, e.DocumentID, e.CategoryID, e.PersonID, e.Amount, e.IncomeDate, e.OriginalDate,
			e.Description, e.Reviewed, e.USDAmount, e.ExchangeRate, categorySourceOrNone(e.CategorySource))
		return err
	})
	if err != nil {
		return translateWriteError(err, "inserting income entry")
	}
	return nil
}

func (r *Repository) Update(ctx context.Context, e income.Entry) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE income_entries
			SET document_id = $2, category_id = $3, person_id = $4, amount = $5::numeric,
			    income_date = $6, original_date = $7, description = $8, reviewed = $9,
			    usd_amount = $10::numeric, exchange_rate = $11::numeric, category_source = $12
			WHERE id = $1
		`, e.ID, e.DocumentID, e.CategoryID, e.PersonID, e.Amount, e.IncomeDate, e.OriginalDate,
			e.Description, e.Reviewed, e.USDAmount, e.ExchangeRate, categorySourceOrNone(e.CategorySource))
		return err
	})
	if err != nil {
		return translateWriteError(err, "updating income entry")
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM income_entries WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting income entry: %w", err)
		}
		return nil
	})
}

// ListByDocument returns every income entry linked to documentID, ordered by original date (nulls
// last) — the provento/desconto lines of one imported paystub.
func (r *Repository) ListByDocument(ctx context.Context, documentID uuid.UUID) ([]income.Entry, error) {
	var result []income.Entry
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, document_id, category_id, person_id, amount::text, income_date, original_date,
			       description, reviewed, created_at, usd_amount::text, exchange_rate::text, source,
			       category_source
			FROM income_entries
			WHERE document_id = $1
			ORDER BY original_date NULLS LAST, created_at
		`, documentID)
		if err != nil {
			return fmt.Errorf("querying income entries by document: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEntry(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

// ReplaceForDocument deletes every income entry currently linked to documentID and inserts es in
// one transaction — a paystub re-extraction is all-or-nothing.
//
// The bank sync is the source of truth for payroll: a paystub's lines are proventos/descontos whose
// SUM is the net the employer deposits, and the sync already stores that net as payroll credits in
// the same month. When that is the case the detailed lines are NOT inserted a second time, so the
// salary is not counted twice (task atlas-openfinance-document-dedup).
func (r *Repository) ReplaceForDocument(ctx context.Context, documentID uuid.UUID, es []income.Entry) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM income_entries WHERE document_id = $1`, documentID); err != nil {
			return fmt.Errorf("clearing existing income lines: %w", err)
		}
		if len(es) > 0 {
			covered, err := payrollCoveredBySync(ctx, tx, es)
			if err != nil {
				return err
			}
			if covered {
				return nil
			}
		}
		for _, e := range es {
			if _, err := tx.Exec(ctx, `
			INSERT INTO income_entries (id, document_id, category_id, person_id, amount, income_date,
			                             original_date, description, reviewed, usd_amount, exchange_rate,
			                             category_source)
				VALUES ($1, $2, $3, $4, $5::numeric, $6, $7, $8, $9, $10::numeric, $11::numeric, $12)
			`, e.ID, e.DocumentID, e.CategoryID, e.PersonID, e.Amount, e.IncomeDate, e.OriginalDate,
				e.Description, e.Reviewed, e.USDAmount, e.ExchangeRate, categorySourceOrNone(e.CategorySource)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return translateWriteError(err, "replacing income lines for document")
	}
	return nil
}

// payrollCoveredBySync reports whether the bank sync already stores a paystub's net as payroll
// credits in the same month. The candidate credits are the synced (`PLUGGY`) entries of that month
// whose description reads like the employer's deposit (salary, vacation, PLR/13º — Pluggy exposes no
// structured "payroll" flag, so the text is the signal). Their sum is compared with the paystub's
// net with a small tolerance, because the bank credit occasionally carries a few reais the paystub
// doesn't itemize (or vice versa). A bigger gap means a genuinely different amount and the paystub
// is kept.
func payrollCoveredBySync(ctx context.Context, tx pgx.Tx, es []income.Entry) (bool, error) {
	total := new(big.Rat)
	for _, e := range es {
		v, ok := new(big.Rat).SetString(e.Amount)
		if !ok {
			return false, nil
		}
		total.Add(total, v)
	}
	if total.Sign() == 0 {
		return false, nil
	}

	month := es[0].IncomeDate.UTC()
	from := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)

	rows, err := tx.Query(ctx, `
		SELECT amount::text, COALESCE(description, '')
		FROM income_entries
		WHERE source = 'PLUGGY' AND bank_transaction_id IS NOT NULL
		  AND income_date >= $1 AND income_date < $2
	`, from, to)
	if err != nil {
		return false, fmt.Errorf("loading synced payroll credits: %w", err)
	}
	defer rows.Close()

	sum := new(big.Rat)
	for rows.Next() {
		var amount, desc string
		if err := rows.Scan(&amount, &desc); err != nil {
			return false, err
		}
		if !isPayrollCredit(desc) {
			continue
		}
		if v, ok := new(big.Rat).SetString(amount); ok {
			sum.Add(sum, v)
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if sum.Sign() == 0 {
		return false, nil
	}

	diff := new(big.Rat).Sub(total, sum)
	diff.Abs(diff)
	tolerance := new(big.Rat).SetFrac64(5, 1) // R$ 5,00
	if pct := new(big.Rat).Mul(new(big.Rat).Abs(total), big.NewRat(5, 1000)); pct.Cmp(tolerance) > 0 {
		tolerance = pct // 0,5% quando o líquido é grande
	}
	return diff.Cmp(tolerance) <= 0, nil
}

// isPayrollCredit recognises the employer's deposits in the bank feed. Pluggy gives no structured
// "this is payroll" flag, so the description is the only signal available.
func isPayrollCredit(description string) bool {
	d := strings.ToUpper(description)
	for _, kw := range []string{"SALAR", "SALÁR", "FOLHA", "PARTICIP", "FERIA", "FÉRIA", "PLR"} {
		if strings.Contains(d, kw) {
			return true
		}
	}
	return false
}

// ListInRange returns every income entry with income_date in [from, toExclusive), joined with its
// category's name — backs MonthlySummary/AnnualSummary.
func (r *Repository) ListInRange(ctx context.Context, from, toExclusive time.Time) ([]income.EntryWithCategoryName, error) {
	var result []income.EntryWithCategoryName
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT e.id, e.document_id, e.category_id, e.person_id, e.amount::text, e.income_date,
			       e.original_date, e.description, e.reviewed, e.created_at,
			       e.usd_amount::text, e.exchange_rate::text, e.source,
			       COALESCE(c.name, 'Sem categoria')
			FROM income_entries e
			LEFT JOIN categories c ON c.id = e.category_id
			WHERE e.income_date >= $1 AND e.income_date < $2
		`, from, toExclusive)
		if err != nil {
			return fmt.Errorf("querying income entries in range: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e income.EntryWithCategoryName
			if err := rows.Scan(&e.ID, &e.DocumentID, &e.CategoryID, &e.PersonID, &e.Amount, &e.IncomeDate,
				&e.OriginalDate, &e.Description, &e.Reviewed, &e.CreatedAt, &e.USDAmount, &e.ExchangeRate,
				&e.Source, &e.CategoryName); err != nil {
				return fmt.Errorf("scanning income entry: %w", err)
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

// translateWriteError turns a foreign-key violation into an apperr.Validation instead of a raw
// 500 — same pattern as internal/expenses/postgres/repository.go.
func translateWriteError(err error, action string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
		return apperr.Validation("documentId, categoryId, or personId does not reference an existing row")
	}
	return fmt.Errorf("%s: %w", action, err)
}

// rowScanner is satisfied by both pgx.Rows and pgx.Row.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanEntry(row rowScanner) (income.Entry, error) {
	var e income.Entry
	err := row.Scan(&e.ID, &e.DocumentID, &e.CategoryID, &e.PersonID, &e.Amount, &e.IncomeDate,
		&e.OriginalDate, &e.Description, &e.Reviewed, &e.CreatedAt, &e.USDAmount, &e.ExchangeRate, &e.Source, &e.CategorySource)
	return e, err
}

// CategoryHistory loads every classified income entry as a category-memory example — see
// internal/expenses/postgres/repository.go.
func (r *Repository) CategoryHistory(ctx context.Context) ([]categorize.Example, error) {
	var result []categorize.Example
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT COALESCE(e.description, ''), c.name, e.category_source = 'MANUAL'
			FROM income_entries e
			JOIN categories c ON c.id = e.category_id
			WHERE e.category_id IS NOT NULL AND COALESCE(e.description, '') <> ''
		`)
		if err != nil {
			return fmt.Errorf("querying income category history: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var example categorize.Example
			if err := rows.Scan(&example.Description, &example.Category, &example.Manual); err != nil {
				return fmt.Errorf("scanning income category history: %w", err)
			}
			result = append(result, example)
		}
		return rows.Err()
	})
	return result, err
}

// CategoryIDsByName returns a lower(name)->id lookup for the categories of one type.
func (r *Repository) CategoryIDsByName(ctx context.Context, categoryType string) (map[string]uuid.UUID, error) {
	result := map[string]uuid.UUID{}
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT lower(name), id FROM categories WHERE type = $1`, categoryType)
		if err != nil {
			return fmt.Errorf("querying categories by name: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var id uuid.UUID
			if err := rows.Scan(&name, &id); err != nil {
				return fmt.Errorf("scanning category by name: %w", err)
			}
			result[name] = id
		}
		return rows.Err()
	})
	return result, err
}

// AutoCategorize applies assigns in one statement, leaving MANUAL rows untouched.
func (r *Repository) AutoCategorize(ctx context.Context, assigns []income.CategoryAssignment) (int, error) {
	if len(assigns) == 0 {
		return 0, nil
	}
	ids := make([]uuid.UUID, len(assigns))
	categoryIDs := make([]uuid.UUID, len(assigns))
	for i, a := range assigns {
		ids[i], categoryIDs[i] = a.ID, a.CategoryID
	}
	var updated int64
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE income_entries e
			SET category_id = v.category_id, category_source = 'HISTORY'
			FROM unnest($1::uuid[], $2::uuid[]) AS v(id, category_id)
			WHERE e.id = v.id AND e.category_source <> 'MANUAL'
		`, ids, categoryIDs)
		if err != nil {
			return err
		}
		updated = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, translateWriteError(err, "autocategorizing income entries")
	}
	return int(updated), nil
}
