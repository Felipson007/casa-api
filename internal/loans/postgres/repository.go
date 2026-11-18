// Package postgres implements loans.Repository — the only file in the loans module that imports
// a SQL driver. Every query goes through db.WithTx. Money columns (numeric(15,2)/numeric(12,6))
// are read/written as text (::text casts, string bind params) rather than float64, to avoid
// floating-point rounding on currency — see internal/loans/domain.go's field comments.
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

	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ loans.Repository = (*Repository)(nil)
var _ loans.MaintenanceRepository = (*Repository)(nil)

func (r *Repository) List(ctx context.Context, clientID *uuid.UUID, query string) ([]loans.Loan, error) {
	var result []loans.Loan
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, client_id, principal_amount::text, monthly_interest_rate::text, payment_type,
			       installment_count, contract_date, status, outstanding_balance::text, due_day,
			       paid_off_date, legacy_source, created_at
			FROM loans l
			WHERE ($1::uuid IS NULL OR l.client_id = $1)
			  AND ($2 = '' OR EXISTS (
			      SELECT 1 FROM clients c WHERE c.id = l.client_id
			      AND (c.name ILIKE '%' || $2 || '%' OR COALESCE(c.email, '') ILIKE '%' || $2 || '%')
			  ))
			ORDER BY contract_date DESC
		`, clientID, query)
		if err != nil {
			return fmt.Errorf("querying loans: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			l, err := scanLoan(rows)
			if err != nil {
				return err
			}
			result = append(result, l)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*loans.Loan, error) {
	var result *loans.Loan
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, client_id, principal_amount::text, monthly_interest_rate::text, payment_type,
			       installment_count, contract_date, status, outstanding_balance::text, due_day,
			       paid_off_date, legacy_source, created_at
			FROM loans
			WHERE id = $1
		`, id)
		l, err := scanLoan(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &l
		return nil
	})
	return result, err
}

func (r *Repository) CreateWithSchedule(ctx context.Context, l loans.Loan, installments []loans.Installment, disbursement loans.Transaction) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO loans (id, client_id, principal_amount, monthly_interest_rate, payment_type,
			                    installment_count, contract_date, status, outstanding_balance, due_day,
			                    paid_off_date, legacy_source)
			VALUES ($1, $2, $3::numeric, $4::numeric, $5::varchar, $6, $7, $8::varchar, $9::numeric, $10, $11, $12)
		`, l.ID, l.ClientID, l.PrincipalAmount, l.MonthlyInterestRate, string(l.PaymentType),
			l.InstallmentCount, l.ContractDate, string(l.Status), l.OutstandingBalance, l.DueDay,
			l.PaidOffDate, l.LegacySource); err != nil {
			return fmt.Errorf("inserting loan: %w", err)
		}

		for _, i := range installments {
			if _, err := tx.Exec(ctx, `
				INSERT INTO installments (id, loan_id, installment_number, due_date, installment_amount,
				                           outstanding_balance, principal_amount, interest_amount, status)
				VALUES ($1, $2, $3, $4, $5::numeric, $6::numeric, $7::numeric, $8::numeric, $9::varchar)
			`, i.ID, i.LoanID, i.InstallmentNumber, i.DueDate, i.InstallmentAmount, i.OutstandingBalance,
				i.PrincipalAmount, i.InterestAmount, string(i.Status)); err != nil {
				return fmt.Errorf("inserting installment #%d: %w", i.InstallmentNumber, err)
			}
		}

		if err := insertTransaction(ctx, tx, disbursement); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return translateWriteError(err, "creating loan with schedule")
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM loans WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting loan: %w", err)
		}
		return nil
	})
}

func (r *Repository) HasPaidInstallment(ctx context.Context, loanID uuid.UUID) (bool, error) {
	var exists bool
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM installments WHERE loan_id = $1 AND status = 'PAID')
		`, loanID).Scan(&exists)
	})
	return exists, err
}

func (r *Repository) HasTransactionBeyondDisbursement(ctx context.Context, loanID uuid.UUID) (bool, error) {
	var exists bool
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM loan_transactions WHERE loan_id = $1 AND type != 'DISBURSEMENT')
		`, loanID).Scan(&exists)
	})
	return exists, err
}

func (r *Repository) ListInstallmentsByLoan(ctx context.Context, loanID uuid.UUID) ([]loans.Installment, error) {
	var result []loans.Installment
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, loan_id, installment_number, due_date, installment_amount::text,
			       outstanding_balance::text, principal_amount::text, interest_amount::text, status,
			       created_at
			FROM installments
			WHERE loan_id = $1
			ORDER BY installment_number
		`, loanID)
		if err != nil {
			return fmt.Errorf("querying installments: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			i, err := scanInstallment(rows)
			if err != nil {
				return err
			}
			result = append(result, i)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindInstallmentByID(ctx context.Context, id uuid.UUID) (*loans.Installment, error) {
	var result *loans.Installment
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, loan_id, installment_number, due_date, installment_amount::text,
			       outstanding_balance::text, principal_amount::text, interest_amount::text, status,
			       created_at
			FROM installments
			WHERE id = $1
		`, id)
		i, err := scanInstallment(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &i
		return nil
	})
	return result, err
}

func (r *Repository) ListOpenInstallmentsAfter(ctx context.Context, loanID uuid.UUID, after time.Time) ([]loans.Installment, error) {
	var result []loans.Installment
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, loan_id, installment_number, due_date, installment_amount::text,
			       outstanding_balance::text, principal_amount::text, interest_amount::text, status,
			       created_at
			FROM installments
			WHERE loan_id = $1 AND status IN ('PENDING', 'OVERDUE') AND due_date > $2
			ORDER BY installment_number
		`, loanID, after)
		if err != nil {
			return fmt.Errorf("querying open installments: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			i, err := scanInstallment(rows)
			if err != nil {
				return err
			}
			result = append(result, i)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) MarkInstallmentPaid(ctx context.Context, installmentID uuid.UUID, loanUpdate *loans.LoanBalanceUpdate, txn loans.Transaction) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE installments SET status = 'PAID' WHERE id = $1`, installmentID); err != nil {
			return fmt.Errorf("marking installment paid: %w", err)
		}
		if loanUpdate != nil {
			if err := applyLoanBalanceUpdate(ctx, tx, *loanUpdate); err != nil {
				return err
			}
		}
		return insertTransaction(ctx, tx, txn)
	})
	if err != nil {
		return fmt.Errorf("recording installment payment: %w", err)
	}
	return nil
}

func (r *Repository) ApplyAmortization(ctx context.Context, loanUpdate loans.LoanBalanceUpdate, deleteInstallmentIDs []uuid.UUID, recalculated []loans.Installment, txn loans.Transaction) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if err := applyLoanBalanceUpdate(ctx, tx, loanUpdate); err != nil {
			return err
		}
		for _, id := range deleteInstallmentIDs {
			if _, err := tx.Exec(ctx, `DELETE FROM installments WHERE id = $1`, id); err != nil {
				return fmt.Errorf("deleting installment %s: %w", id, err)
			}
		}
		for _, inst := range recalculated {
			if _, err := tx.Exec(ctx, `
				UPDATE installments
				SET interest_amount = $2::numeric, installment_amount = $3::numeric, outstanding_balance = $4::numeric
				WHERE id = $1
			`, inst.ID, inst.InterestAmount, inst.InstallmentAmount, inst.OutstandingBalance); err != nil {
				return fmt.Errorf("recalculating installment %s: %w", inst.ID, err)
			}
		}
		return insertTransaction(ctx, tx, txn)
	})
	if err != nil {
		return fmt.Errorf("applying amortization: %w", err)
	}
	return nil
}

func (r *Repository) RecordTransaction(ctx context.Context, t loans.Transaction) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return insertTransaction(ctx, tx, t)
	})
	if err != nil {
		return translateWriteError(err, "recording transaction")
	}
	return nil
}

func (r *Repository) FindTransactionByID(ctx context.Context, id uuid.UUID) (*loans.Transaction, error) {
	var result *loans.Transaction
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, client_id, loan_id, installment_id, type, amount::text, payment_date,
			       description, legacy_source, created_at
			FROM loan_transactions
			WHERE id = $1
		`, id)
		t, err := scanTransaction(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &t
		return nil
	})
	return result, err
}

func (r *Repository) ListTransactionsByLoan(ctx context.Context, loanID uuid.UUID) ([]loans.Transaction, error) {
	var result []loans.Transaction
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, client_id, loan_id, installment_id, type, amount::text, payment_date,
			       description, legacy_source, created_at
			FROM loan_transactions
			WHERE loan_id = $1
			ORDER BY payment_date, created_at
		`, loanID)
		if err != nil {
			return fmt.Errorf("querying loan_transactions: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			t, err := scanTransaction(rows)
			if err != nil {
				return err
			}
			result = append(result, t)
		}
		return rows.Err()
	})
	return result, err
}

// ListAllTransactions is the flat, cross-loan ledger backing GET /api/transactions — one query,
// optionally filtered to a single client, most recent first. See loans.Repository's doc comment
// on why this exists: it replaces a frontend fan-out of one request per loan.
func (r *Repository) ListAllTransactions(ctx context.Context, clientID *uuid.UUID) ([]loans.Transaction, error) {
	var result []loans.Transaction
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, client_id, loan_id, installment_id, type, amount::text, payment_date,
			       description, legacy_source, created_at
			FROM loan_transactions
			WHERE $1::uuid IS NULL OR client_id = $1
			ORDER BY payment_date DESC, created_at DESC, id DESC
		`, clientID)
		if err != nil {
			return fmt.Errorf("querying loan_transactions: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			t, err := scanTransaction(rows)
			if err != nil {
				return err
			}
			result = append(result, t)
		}
		return rows.Err()
	})
	return result, err
}

// applyLoanBalanceUpdate is shared by MarkInstallmentPaid and ApplyAmortization — both change the
// loan's outstanding_balance/status/paid_off_date the same way.
func applyLoanBalanceUpdate(ctx context.Context, tx pgx.Tx, u loans.LoanBalanceUpdate) error {
	_, err := tx.Exec(ctx, `
		UPDATE loans SET outstanding_balance = $2::numeric, status = $3::varchar, paid_off_date = $4
		WHERE id = $1
	`, u.LoanID, u.NewBalance, string(u.NewStatus), u.PaidOffDate)
	if err != nil {
		return fmt.Errorf("updating loan balance: %w", err)
	}
	return nil
}

func insertTransaction(ctx context.Context, tx pgx.Tx, t loans.Transaction) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO loan_transactions (id, client_id, loan_id, installment_id, type, amount,
		                                payment_date, description, legacy_source)
		VALUES ($1, $2, $3, $4, $5::varchar, $6::numeric, $7, $8, $9)
	`, t.ID, t.ClientID, t.LoanID, t.InstallmentID, string(t.Type), t.Amount, t.PaymentDate,
		t.Description, t.LegacySource)
	if err != nil {
		return fmt.Errorf("inserting loan_transaction: %w", err)
	}
	return nil
}

func (r *Repository) ListActiveRecurringInterestLoans(ctx context.Context) ([]loans.Loan, error) {
	var result []loans.Loan
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, client_id, principal_amount::text, monthly_interest_rate::text, payment_type,
			       installment_count, contract_date, status, outstanding_balance::text, due_day,
			       paid_off_date, legacy_source, created_at
			FROM loans
			WHERE status = 'ACTIVE' AND payment_type = 'RECURRING_INTEREST'
		`)
		if err != nil {
			return fmt.Errorf("querying active recurring-interest loans: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			l, err := scanLoan(rows)
			if err != nil {
				return err
			}
			result = append(result, l)
		}
		return rows.Err()
	})
	return result, err
}

// ExtendRecurringSchedule persists newly generated installments and updates the loan's
// installment_count, in one transaction.
func (r *Repository) ExtendRecurringSchedule(ctx context.Context, loanID uuid.UUID, newInstallments []loans.Installment, newInstallmentCount int) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		for _, i := range newInstallments {
			if _, err := tx.Exec(ctx, `
				INSERT INTO installments (id, loan_id, installment_number, due_date, installment_amount,
				                           outstanding_balance, principal_amount, interest_amount, status)
				VALUES ($1, $2, $3, $4, $5::numeric, $6::numeric, $7::numeric, $8::numeric, $9::varchar)
			`, i.ID, i.LoanID, i.InstallmentNumber, i.DueDate, i.InstallmentAmount, i.OutstandingBalance,
				i.PrincipalAmount, i.InterestAmount, string(i.Status)); err != nil {
				return fmt.Errorf("inserting rollover installment #%d: %w", i.InstallmentNumber, err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE loans SET installment_count = $2 WHERE id = $1`, loanID, newInstallmentCount); err != nil {
			return fmt.Errorf("updating installment_count: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("extending recurring schedule: %w", err)
	}
	return nil
}

func (r *Repository) ListOverdueCandidates(ctx context.Context, today time.Time) ([]loans.Installment, error) {
	var result []loans.Installment
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, loan_id, installment_number, due_date, installment_amount::text,
			       outstanding_balance::text, principal_amount::text, interest_amount::text, status,
			       created_at
			FROM installments
			WHERE status = 'PENDING' AND due_date < $1
		`, today)
		if err != nil {
			return fmt.Errorf("querying overdue candidates: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			i, err := scanInstallment(rows)
			if err != nil {
				return err
			}
			result = append(result, i)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) MarkInstallmentsOverdue(ctx context.Context, installmentIDs []uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE installments SET status = 'OVERDUE' WHERE id = ANY($1)`, installmentIDs)
		if err != nil {
			return fmt.Errorf("marking installments overdue: %w", err)
		}
		return nil
	})
}

func (r *Repository) ListDueSoonCandidates(ctx context.Context, targetDate time.Time) ([]loans.Installment, error) {
	var result []loans.Installment
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, loan_id, installment_number, due_date, installment_amount::text,
			       outstanding_balance::text, principal_amount::text, interest_amount::text, status,
			       created_at
			FROM installments
			WHERE status = 'PENDING' AND due_date = $1
		`, targetDate)
		if err != nil {
			return fmt.Errorf("querying due-soon candidates: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			i, err := scanInstallment(rows)
			if err != nil {
				return err
			}
			result = append(result, i)
		}
		return rows.Err()
	})
	return result, err
}

// ListRelevantForVigentes returns every ACTIVE or PAID_OFF loan with its client's name resolved via
// a JOIN — mirrors EmprestimoRepository.findAllByStatusIn(ATIVO, QUITADO).
func (r *Repository) ListRelevantForVigentes(ctx context.Context) ([]loans.LoanWithClientName, error) {
	var result []loans.LoanWithClientName
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT l.id, l.client_id, l.principal_amount::text, l.monthly_interest_rate::text, l.payment_type,
			       l.installment_count, l.contract_date, l.status, l.outstanding_balance::text, l.due_day,
			       l.paid_off_date, l.legacy_source, l.created_at, c.name
			FROM loans l
			JOIN clients c ON c.id = l.client_id
			WHERE l.status IN ('ACTIVE', 'PAID_OFF')
		`)
		if err != nil {
			return fmt.Errorf("querying relevant loans for vigentes: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var lc loans.LoanWithClientName
			if err := rows.Scan(&lc.ID, &lc.ClientID, &lc.PrincipalAmount, &lc.MonthlyInterestRate, &lc.PaymentType,
				&lc.InstallmentCount, &lc.ContractDate, &lc.Status, &lc.OutstandingBalance, &lc.DueDay,
				&lc.PaidOffDate, &lc.LegacySource, &lc.CreatedAt, &lc.ClientName); err != nil {
				return fmt.Errorf("scanning loan for vigentes: %w", err)
			}
			result = append(result, lc)
		}
		return rows.Err()
	})
	return result, err
}

// ListInstallmentsDueInRange returns every installment, for any of loanIDs, due in
// [from, toExclusive) — one bulk query (= ANY($1)), not one per loan.
func (r *Repository) ListInstallmentsDueInRange(ctx context.Context, loanIDs []uuid.UUID, from, toExclusive time.Time) ([]loans.Installment, error) {
	if len(loanIDs) == 0 {
		return nil, nil
	}
	var result []loans.Installment
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, loan_id, installment_number, due_date, installment_amount::text,
			       outstanding_balance::text, principal_amount::text, interest_amount::text, status,
			       created_at
			FROM installments
			WHERE loan_id = ANY($1) AND due_date >= $2 AND due_date < $3
		`, loanIDs, from, toExclusive)
		if err != nil {
			return fmt.Errorf("querying installments due in range: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			i, err := scanInstallment(rows)
			if err != nil {
				return err
			}
			result = append(result, i)
		}
		return rows.Err()
	})
	return result, err
}

// ListInstallmentPaymentsByDueDate returns, for every INSTALLMENT_PAYMENT transaction whose linked
// installment is due in [from, toExclusive), one row per (loan, installment due month) with the
// summed amount — mirrors buscarRecebimentoParcelaPorEmprestimoEDataVencimentoEntre: bucketed by
// the INSTALLMENT's due date, not the transaction's own payment_date.
func (r *Repository) ListInstallmentPaymentsByDueDate(ctx context.Context, from, toExclusive time.Time) ([]loans.LoanAmountRow, error) {
	var result []loans.LoanAmountRow
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT t.loan_id, date_trunc('month', i.due_date) AS month, sum(t.amount)::text
			FROM loan_transactions t
			JOIN installments i ON i.id = t.installment_id
			WHERE t.type = 'INSTALLMENT_PAYMENT' AND i.due_date >= $1 AND i.due_date < $2
			GROUP BY t.loan_id, date_trunc('month', i.due_date)
		`, from, toExclusive)
		if err != nil {
			return fmt.Errorf("querying installment payments by due date: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			row, err := scanLoanAmountRow(rows)
			if err != nil {
				return err
			}
			result = append(result, row)
		}
		return rows.Err()
	})
	return result, err
}

// ListOtherPaymentsByPaymentDate returns, for every AMORTIZATION/ONE_OFF_PAYMENT transaction with a
// payment_date in [from, toExclusive), one row per (loan, payment month) with the summed amount —
// mirrors buscarValorPorTipoEEmprestimoEDataPagamentoEntre.
func (r *Repository) ListOtherPaymentsByPaymentDate(ctx context.Context, from, toExclusive time.Time) ([]loans.LoanAmountRow, error) {
	var result []loans.LoanAmountRow
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT loan_id, date_trunc('month', payment_date) AS month, sum(amount)::text
			FROM loan_transactions
			WHERE type IN ('AMORTIZATION', 'ONE_OFF_PAYMENT') AND loan_id IS NOT NULL
			  AND payment_date >= $1 AND payment_date < $2
			GROUP BY loan_id, date_trunc('month', payment_date)
		`, from, toExclusive)
		if err != nil {
			return fmt.Errorf("querying other payments by payment date: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			row, err := scanLoanAmountRow(rows)
			if err != nil {
				return err
			}
			result = append(result, row)
		}
		return rows.Err()
	})
	return result, err
}

// ListRecentTransactions returns the most recent loan_transactions across every loan, joined with
// the client's name — backs the dashboard's "recent activity" feed.
func (r *Repository) ListRecentTransactions(ctx context.Context, limit int) ([]loans.TransactionWithClientName, error) {
	var result []loans.TransactionWithClientName
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT t.id, t.client_id, t.loan_id, t.installment_id, t.type, t.amount::text, t.payment_date,
			       t.description, t.legacy_source, t.created_at, c.name
			FROM loan_transactions t
			JOIN clients c ON c.id = t.client_id
			ORDER BY t.created_at DESC
			LIMIT $1
		`, limit)
		if err != nil {
			return fmt.Errorf("querying recent transactions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var t loans.TransactionWithClientName
			if err := rows.Scan(&t.ID, &t.ClientID, &t.LoanID, &t.InstallmentID, &t.Type, &t.Amount,
				&t.PaymentDate, &t.Description, &t.LegacySource, &t.CreatedAt, &t.ClientName); err != nil {
				return fmt.Errorf("scanning recent transaction: %w", err)
			}
			result = append(result, t)
		}
		return rows.Err()
	})
	return result, err
}

// SumOneOffAndAmortizationByMonth returns the same underlying rows as ListOtherPaymentsByPaymentDate
// but split by type — backs CalculateKPIs, which needs amortizacoesNoMes as its own field.
func (r *Repository) SumOneOffAndAmortizationByMonth(ctx context.Context, from, toExclusive time.Time) (map[loans.YearMonth]string, map[loans.YearMonth]string, error) {
	oneOff := make(map[loans.YearMonth]string)
	amortization := make(map[loans.YearMonth]string)
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT type, date_trunc('month', payment_date) AS month, sum(amount)::text
			FROM loan_transactions
			WHERE type IN ('AMORTIZATION', 'ONE_OFF_PAYMENT') AND loan_id IS NOT NULL
			  AND payment_date >= $1 AND payment_date < $2
			GROUP BY type, date_trunc('month', payment_date)
		`, from, toExclusive)
		if err != nil {
			return fmt.Errorf("querying one-off/amortization sums: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var txnType string
			var month time.Time
			var amount string
			if err := rows.Scan(&txnType, &month, &amount); err != nil {
				return fmt.Errorf("scanning one-off/amortization row: %w", err)
			}
			ym := loans.YearMonth{Year: month.Year(), Month: int(month.Month())}
			if txnType == "AMORTIZATION" {
				amortization[ym] = amount
			} else {
				oneOff[ym] = amount
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, nil, err
	}
	return oneOff, amortization, nil
}

func scanLoanAmountRow(row rowScanner) (loans.LoanAmountRow, error) {
	var out loans.LoanAmountRow
	var month time.Time
	var loanID *uuid.UUID
	if err := row.Scan(&loanID, &month, &out.Amount); err != nil {
		return out, fmt.Errorf("scanning loan amount row: %w", err)
	}
	if loanID != nil {
		out.LoanID = *loanID
	}
	out.Month = loans.YearMonth{Year: month.Year(), Month: int(month.Month())}
	return out, nil
}

// UpdateFull replaces a loan's terms outright: deletes every existing installment (cascades
// naturally on loan_transactions.installment_id via ON DELETE SET NULL, so those rows survive as
// orphaned references — deleted explicitly below instead, since UpdateFull's whole point is a
// clean slate) and transaction, updates the loan row, and inserts the freshly generated
// installments + disbursement transaction — all in one transaction, same reasoning as
// CreateWithSchedule.
func (r *Repository) UpdateFull(ctx context.Context, l loans.Loan, installments []loans.Installment, disbursement loans.Transaction) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM loan_transactions WHERE loan_id = $1`, l.ID); err != nil {
			return fmt.Errorf("deleting existing transactions: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM installments WHERE loan_id = $1`, l.ID); err != nil {
			return fmt.Errorf("deleting existing installments: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE loans SET client_id = $2, principal_amount = $3::numeric, monthly_interest_rate = $4::numeric,
			       payment_type = $5::varchar, installment_count = $6, contract_date = $7, status = $8::varchar,
			       outstanding_balance = $9::numeric, due_day = $10, paid_off_date = $11, legacy_source = $12
			WHERE id = $1
		`, l.ID, l.ClientID, l.PrincipalAmount, l.MonthlyInterestRate, string(l.PaymentType),
			l.InstallmentCount, l.ContractDate, string(l.Status), l.OutstandingBalance, l.DueDay,
			l.PaidOffDate, l.LegacySource); err != nil {
			return fmt.Errorf("updating loan: %w", err)
		}
		for _, i := range installments {
			if _, err := tx.Exec(ctx, `
				INSERT INTO installments (id, loan_id, installment_number, due_date, installment_amount,
				                           outstanding_balance, principal_amount, interest_amount, status)
				VALUES ($1, $2, $3, $4, $5::numeric, $6::numeric, $7::numeric, $8::numeric, $9::varchar)
			`, i.ID, i.LoanID, i.InstallmentNumber, i.DueDate, i.InstallmentAmount, i.OutstandingBalance,
				i.PrincipalAmount, i.InterestAmount, string(i.Status)); err != nil {
				return fmt.Errorf("inserting installment #%d: %w", i.InstallmentNumber, err)
			}
		}
		return insertTransaction(ctx, tx, disbursement)
	})
	if err != nil {
		return translateWriteError(err, "updating loan with new schedule")
	}
	return nil
}

// Cancel sets a loan's status to CANCELLED — no other side effect (installments/transactions are
// left exactly as they are, unlike Delete/UpdateFull).
func (r *Repository) Cancel(ctx context.Context, loanID uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE loans SET status = 'CANCELLED' WHERE id = $1`, loanID)
		if err != nil {
			return fmt.Errorf("cancelling loan: %w", err)
		}
		return nil
	})
}

// ChangeDueDay updates the loan's due_day and overwrites every shiftedInstallments row's due_date
// (already recalculated by Service.ChangeDueDay) in one transaction.
func (r *Repository) ChangeDueDay(ctx context.Context, loanID uuid.UUID, newDueDay int, shiftedInstallments []loans.Installment) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE loans SET due_day = $2 WHERE id = $1`, loanID, newDueDay); err != nil {
			return fmt.Errorf("updating due day: %w", err)
		}
		for _, inst := range shiftedInstallments {
			if _, err := tx.Exec(ctx, `UPDATE installments SET due_date = $2 WHERE id = $1`, inst.ID, inst.DueDate); err != nil {
				return fmt.Errorf("shifting installment %s: %w", inst.ID, err)
			}
		}
		return nil
	})
}

// PostponeInstallment updates a single installment's due_date — no other row is touched (unlike
// ChangeDueDay, which rewrites every open installment plus the loan's own due_day). Also resets
// status to PENDING: the daily maintenance sweep (maintenance.go's markOverdueInstallments) only
// ever moves PENDING -> OVERDUE, never back — so an OVERDUE installment postponed into the future
// would otherwise stay stuck showing "atrasada" even though it isn't due yet anymore. If the new
// date also lapses unpaid, the same sweep will correctly mark it OVERDUE again on its own.
func (r *Repository) PostponeInstallment(ctx context.Context, installmentID uuid.UUID, newDueDate time.Time) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE installments SET due_date = $2, status = 'PENDING' WHERE id = $1`, installmentID, newDueDate); err != nil {
			return fmt.Errorf("postponing installment: %w", err)
		}
		return nil
	})
}

func (r *Repository) ChangeInterestRate(ctx context.Context, loanID uuid.UUID, newRate string, recalculated []loans.Installment) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE loans SET monthly_interest_rate = $2::numeric WHERE id = $1`, loanID, newRate); err != nil {
			return fmt.Errorf("updating interest rate: %w", err)
		}
		for _, inst := range recalculated {
			if _, err := tx.Exec(ctx, `
				UPDATE installments
				SET interest_amount = $2::numeric, installment_amount = $3::numeric
				WHERE id = $1
			`, inst.ID, inst.InterestAmount, inst.InstallmentAmount); err != nil {
				return fmt.Errorf("recalculating installment %s: %w", inst.ID, err)
			}
		}
		return nil
	})
}

// translateWriteError turns a foreign-key violation (client_id referencing a nonexistent client)
// into an apperr.Validation instead of a raw 500 — mirrors how apperr.md describes translating
// sqlc/pgx errors at the repository boundary (docs/architecture-plan.md section 5).
func translateWriteError(err error, action string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
		return apperr.Validation("clientId does not reference an existing client", "clientId")
	}
	return fmt.Errorf("%s: %w", action, err)
}

// rowScanner is satisfied by both pgx.Rows and pgx.Row.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanLoan(row rowScanner) (loans.Loan, error) {
	var l loans.Loan
	err := row.Scan(&l.ID, &l.ClientID, &l.PrincipalAmount, &l.MonthlyInterestRate, &l.PaymentType,
		&l.InstallmentCount, &l.ContractDate, &l.Status, &l.OutstandingBalance, &l.DueDay,
		&l.PaidOffDate, &l.LegacySource, &l.CreatedAt)
	return l, err
}

func scanInstallment(row rowScanner) (loans.Installment, error) {
	var i loans.Installment
	err := row.Scan(&i.ID, &i.LoanID, &i.InstallmentNumber, &i.DueDate, &i.InstallmentAmount,
		&i.OutstandingBalance, &i.PrincipalAmount, &i.InterestAmount, &i.Status, &i.CreatedAt)
	return i, err
}

func scanTransaction(row rowScanner) (loans.Transaction, error) {
	var t loans.Transaction
	err := row.Scan(&t.ID, &t.ClientID, &t.LoanID, &t.InstallmentID, &t.Type, &t.Amount,
		&t.PaymentDate, &t.Description, &t.LegacySource, &t.CreatedAt)
	return t, err
}
