// Package postgres implements expenses.Repository — the only file in the expenses module that
// imports a SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/categorize"
	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/platform/textmatch"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ expenses.Repository = (*Repository)(nil)

// categorySourceOrNone keeps category_source inside its CHECK constraint
// (NONE/AI/RULE/HISTORY/MANUAL): callers that build an Expense directly — the document/adoption
// paths and tests — may leave it empty, which the column rejects. The service normalises for the
// HTTP path, but the repository is the last line of defence.
func categorySourceOrNone(s string) string {
	if s == "" {
		return expenses.CategorySourceNone
	}
	return s
}

func (r *Repository) List(ctx context.Context, categoryID, personID *uuid.UUID, query string) ([]expenses.Expense, error) {
	var result []expenses.Expense
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, document_id, category_id, person_id, amount::text, expense_date, description,
			       reviewed, original_date, recurring, created_at, source, category_source
			FROM expenses
			WHERE ($1::uuid IS NULL OR category_id = $1)
			  AND ($2::uuid IS NULL OR person_id = $2)
			  AND ($3 = '' OR COALESCE(description, '') ILIKE '%' || $3 || '%')
			ORDER BY expense_date DESC
		`, categoryID, personID, query)
		if err != nil {
			return fmt.Errorf("querying expenses: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			e, err := scanExpense(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*expenses.Expense, error) {
	var result *expenses.Expense
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, document_id, category_id, person_id, amount::text, expense_date, description,
			       reviewed, original_date, recurring, created_at, source, category_source
			FROM expenses
			WHERE id = $1
		`, id)
		e, err := scanExpense(row)
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

func (r *Repository) Create(ctx context.Context, e expenses.Expense) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO expenses (id, document_id, category_id, person_id, amount, expense_date,
			                       description, reviewed, original_date, recurring, category_source)
			VALUES ($1, $2, $3, $4, $5::numeric, $6, $7, $8, $9, $10, $11)
		`, e.ID, e.DocumentID, e.CategoryID, e.PersonID, e.Amount, e.ExpenseDate, e.Description,
			e.Reviewed, e.OriginalDate, e.Recurring, categorySourceOrNone(e.CategorySource))
		return err
	})
	if err != nil {
		return translateWriteError(err, "inserting expense")
	}
	return nil
}

func (r *Repository) Update(ctx context.Context, e expenses.Expense) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE expenses
			SET document_id = $2, category_id = $3, person_id = $4, amount = $5::numeric,
			    expense_date = $6, description = $7, reviewed = $8, original_date = $9, recurring = $10,
			    category_source = $11
			WHERE id = $1
		`, e.ID, e.DocumentID, e.CategoryID, e.PersonID, e.Amount, e.ExpenseDate, e.Description,
			e.Reviewed, e.OriginalDate, e.Recurring, categorySourceOrNone(e.CategorySource))
		return err
	})
	if err != nil {
		return translateWriteError(err, "updating expense")
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM expenses WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting expense: %w", err)
		}
		return nil
	})
}

// ListByDocument returns every expense linked to documentID, ordered by original purchase date
// (nulls last) — the line items of one imported invoice.
func (r *Repository) ListByDocument(ctx context.Context, documentID uuid.UUID) ([]expenses.Expense, error) {
	var result []expenses.Expense
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, document_id, category_id, person_id, amount::text, expense_date, description,
			       reviewed, original_date, recurring, created_at, source, category_source
			FROM expenses
			WHERE document_id = $1
			ORDER BY original_date NULLS LAST, created_at
		`, documentID)
		if err != nil {
			return fmt.Errorf("querying expenses by document: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanExpense(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

// ReplaceForDocument replaces every expense line of documentID with es in one transaction — an
// invoice re-extraction is all-or-nothing, never a partial overwrite.
//
// De-duplication against the bank sync: a line that is the SAME purchase as an expense the bank
// already synced (same amount, same day give or take two, same merchant — see textmatch.Pick) is
// not inserted a second time. The synced row is ADOPTED instead: it is attached to this document
// (so the invoice still lists — and totals — every one of its lines), takes the statement line's
// dates (the printed purchase date beats a bank feed that stamps every charge with the statement
// day) and, if it had none, the statement's category. The bank remains the source of
// the row; the invoice is only its statement. Re-running releases adopted rows first, and
// deleting the document releases them without deleting the money.
func (r *Repository) ReplaceForDocument(ctx context.Context, documentID uuid.UUID, es []expenses.Expense) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE expenses SET document_id = NULL WHERE document_id = $1 AND source = 'PLUGGY'`, documentID); err != nil {
			return fmt.Errorf("releasing bank-synced lines: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM expenses WHERE document_id = $1`, documentID); err != nil {
			return fmt.Errorf("clearing existing expense lines: %w", err)
		}
		span := statementSpan(es)
		for _, e := range es {
			adopted, err := adoptSyncedExpense(ctx, tx, documentID, e, span)
			if err != nil {
				return err
			}
			if !adopted {
				if adopted, err = adoptSyncedIOF(ctx, tx, documentID, e, span); err != nil {
					return err
				}
			}
			if adopted {
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO expenses (id, document_id, category_id, person_id, amount, expense_date,
				                       description, reviewed, original_date, recurring, source, category_source)
				VALUES ($1, $2, $3, $4, $5::numeric, $6, $7, $8, $9, $10, 'DOCUMENT', $11)
			`, e.ID, e.DocumentID, e.CategoryID, e.PersonID, e.Amount, e.ExpenseDate, e.Description,
				e.Reviewed, e.OriginalDate, e.Recurring, categorySourceOrNone(e.CategorySource)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return translateWriteError(err, "replacing expense lines for document")
	}
	return nil
}

// purchaseSpan is the range of purchase dates printed on one statement (its billing cycle), from
// the lines that are not later installments of an older purchase.
type purchaseSpan struct{ from, to *time.Time }

func statementSpan(es []expenses.Expense) purchaseSpan {
	var span purchaseSpan
	for _, e := range es {
		if e.OriginalDate == nil || !sameDate(*e.OriginalDate, e.ExpenseDate) {
			continue // no purchase date, or an installment counted months after the purchase
		}
		d := *e.OriginalDate
		if span.from == nil || d.Before(*span.from) {
			span.from = &d
		}
		if span.to == nil || d.After(*span.to) {
			span.to = &d
		}
	}
	return span
}

// adoptSyncedExpense attaches an unattached bank-synced CARD expense that is the same purchase as e
// to documentID. Returns false when there is none (the caller inserts e as a new line).
//
// Same two rules as openfinance.linkExisting, in the other direction: the strict ±2-day window,
// then the same-statement rule — a synced card row whose date falls inside this statement's
// billing cycle (span, ±3 days), picked by merchant. The second rule exists because some bank
// feeds date every charge of a statement on one day (the statement date), which the strict
// window can never match.
func adoptSyncedExpense(ctx context.Context, tx pgx.Tx, documentID uuid.UUID, e expenses.Expense, span purchaseSpan) (bool, error) {
	purchase := e.ExpenseDate
	if e.OriginalDate != nil {
		purchase = *e.OriginalDate
	}
	rows, err := tx.Query(ctx, `
		SELECT x.id::text, COALESCE(x.original_date, x.expense_date), COALESCE(x.description, ''),
		       ($3::date IS NOT NULL AND COALESCE(x.original_date, x.expense_date) BETWEEN $3::date - 3 AND $4::date + 3)
		FROM expenses x
		WHERE x.source = 'PLUGGY' AND x.document_id IS NULL AND x.amount = $1::numeric
		  AND (
		        COALESCE(x.original_date, x.expense_date) BETWEEN $2::date - 2 AND $2::date + 2
		        OR ($3::date IS NOT NULL AND COALESCE(x.original_date, x.expense_date) BETWEEN $3::date - 3 AND $4::date + 3)
		      )
		  AND EXISTS (SELECT 1 FROM bank_transactions t JOIN bank_accounts a ON a.id = t.account_id
		              WHERE t.id = x.bank_transaction_id AND a.account_type = 'CREDIT')
		ORDER BY x.created_at
		FOR UPDATE SKIP LOCKED
	`, e.Amount, purchase, span.from, span.to)
	if err != nil {
		return false, fmt.Errorf("looking for a synced duplicate: %w", err)
	}
	var strict, sameStatement []textmatch.Candidate
	for rows.Next() {
		var id, desc string
		var purchaseDate time.Time
		var inCycle bool
		if err := rows.Scan(&id, &purchaseDate, &desc, &inCycle); err != nil {
			rows.Close()
			return false, err
		}
		strict = append(strict, textmatch.Candidate{ID: id, Date: purchaseDate, Description: desc})
		if inCycle {
			sameStatement = append(sameStatement, textmatch.Candidate{ID: id, Description: desc})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	desc := ""
	if e.Description != nil {
		desc = *e.Description
	}
	var chosen string
	if i, ok := textmatch.Pick(strict, purchase, desc); ok {
		chosen = strict[i].ID
	} else if i, ok := textmatch.PickSameStatement(sameStatement, desc); ok {
		chosen = sameStatement[i].ID
	} else {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE expenses
		SET document_id = $2, expense_date = $3, original_date = COALESCE($4, original_date),
		    category_id = COALESCE(category_id, $5), person_id = COALESCE(person_id, $6)
		WHERE id = $1::uuid
	`, chosen, documentID, e.ExpenseDate, e.OriginalDate, e.CategoryID, e.PersonID); err != nil {
		return false, fmt.Errorf("adopting synced expense: %w", err)
	}
	return true, nil
}

// adoptSyncedIOF handles the one line a statement prints as an aggregate: "Repasse de IOF" on
// international purchases is a single line on the PDF, while the bank feed sends one "IOF ..."
// row per purchase. When the unattached synced IOF rows of this billing cycle add up to the line
// exactly, they ARE that line: all of them are attached to the document (keeping their own dates)
// and the aggregate is not inserted a second time.
func adoptSyncedIOF(ctx context.Context, tx pgx.Tx, documentID uuid.UUID, e expenses.Expense, span purchaseSpan) (bool, error) {
	if e.Description == nil || span.from == nil || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(*e.Description)), "repasse de iof") {
		return false, nil
	}
	tag, err := tx.Exec(ctx, `
		WITH iof AS (
			SELECT x.id FROM expenses x
			WHERE x.source = 'PLUGGY' AND x.document_id IS NULL
			  AND upper(COALESCE(x.description, '')) LIKE 'IOF%'
			  AND COALESCE(x.original_date, x.expense_date) BETWEEN $3::date - 3 AND $4::date + 3
			  AND EXISTS (SELECT 1 FROM bank_transactions t JOIN bank_accounts a ON a.id = t.account_id
			              WHERE t.id = x.bank_transaction_id AND a.account_type = 'CREDIT')
		)
		UPDATE expenses SET document_id = $1
		WHERE id IN (SELECT id FROM iof)
		  AND (SELECT sum(amount) FROM expenses WHERE id IN (SELECT id FROM iof)) = $2::numeric
	`, documentID, e.Amount, span.from, span.to)
	if err != nil {
		return false, fmt.Errorf("adopting synced IOF rows: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func sameDate(a, b time.Time) bool {
	return a.UTC().Format("2006-01-02") == b.UTC().Format("2006-01-02")
}

// ListInRange returns every expense with expense_date in [from, toExclusive), joined with its
// category's name ("Sem categoria" when uncategorized) — backs MonthlySummary/AnnualSummary.
func (r *Repository) ListInRange(ctx context.Context, from, toExclusive time.Time) ([]expenses.ExpenseWithCategoryName, error) {
	var result []expenses.ExpenseWithCategoryName
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT e.id, e.document_id, e.category_id, e.person_id, e.amount::text, e.expense_date,
			       e.description, e.reviewed, e.original_date, e.recurring, e.created_at, e.source,
			       COALESCE(c.name, 'Sem categoria')
			FROM expenses e
			LEFT JOIN categories c ON c.id = e.category_id
			WHERE e.expense_date >= $1 AND e.expense_date < $2
		`, from, toExclusive)
		if err != nil {
			return fmt.Errorf("querying expenses in range: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanExpenseWithCategory(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

// ListRecurringSince returns every Recurring=true expense with expense_date >= since, most recent
// first — backs Subscriptions.
func (r *Repository) ListRecurringSince(ctx context.Context, since time.Time) ([]expenses.ExpenseWithCategoryName, error) {
	var result []expenses.ExpenseWithCategoryName
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT e.id, e.document_id, e.category_id, e.person_id, e.amount::text, e.expense_date,
			       e.description, e.reviewed, e.original_date, e.recurring, e.created_at, e.source,
			       COALESCE(c.name, 'Sem categoria')
			FROM expenses e
			LEFT JOIN categories c ON c.id = e.category_id
			WHERE e.recurring = true AND e.expense_date >= $1
			ORDER BY e.expense_date DESC
		`, since)
		if err != nil {
			return fmt.Errorf("querying recurring expenses: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanExpenseWithCategory(rows)
			if err != nil {
				return err
			}
			result = append(result, e)
		}
		return rows.Err()
	})
	return result, err
}

func scanExpenseWithCategory(row rowScanner) (expenses.ExpenseWithCategoryName, error) {
	var e expenses.ExpenseWithCategoryName
	err := row.Scan(&e.ID, &e.DocumentID, &e.CategoryID, &e.PersonID, &e.Amount, &e.ExpenseDate,
		&e.Description, &e.Reviewed, &e.OriginalDate, &e.Recurring, &e.CreatedAt, &e.Source, &e.CategoryName)
	return e, err
}

// translateWriteError turns a foreign-key violation (documentId/categoryId/personId referencing a
// nonexistent row) into an apperr.Validation instead of a raw 500 — same pattern as
// internal/loans/postgres/repository.go.
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

func scanExpense(row rowScanner) (expenses.Expense, error) {
	var e expenses.Expense
	err := row.Scan(&e.ID, &e.DocumentID, &e.CategoryID, &e.PersonID, &e.Amount, &e.ExpenseDate,
		&e.Description, &e.Reviewed, &e.OriginalDate, &e.Recurring, &e.CreatedAt, &e.Source, &e.CategorySource)
	return e, err
}

// CategoryHistory loads every classified expense as a memory example. A human-classified row
// (MANUAL) is flagged so the memory lets it win ties — see internal/categorize.
func (r *Repository) CategoryHistory(ctx context.Context) ([]categorize.Example, error) {
	var result []categorize.Example
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT COALESCE(e.description, ''), c.name, e.category_source = 'MANUAL'
			FROM expenses e
			JOIN categories c ON c.id = e.category_id
			WHERE e.category_id IS NOT NULL AND COALESCE(e.description, '') <> ''
		`)
		if err != nil {
			return fmt.Errorf("querying expense category history: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var example categorize.Example
			if err := rows.Scan(&example.Description, &example.Category, &example.Manual); err != nil {
				return fmt.Errorf("scanning expense category history: %w", err)
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
func (r *Repository) AutoCategorize(ctx context.Context, assigns []expenses.CategoryAssignment) (int, error) {
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
			UPDATE expenses e
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
		return 0, translateWriteError(err, "autocategorizing expenses")
	}
	return int(updated), nil
}
