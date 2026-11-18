//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/expenses"
	expensespostgres "github.com/atlas/atlas-go/internal/expenses/postgres"
	"github.com/atlas/atlas-go/internal/openfinance"
	"github.com/atlas/atlas-go/internal/openfinance/postgres"
	"github.com/atlas/atlas-go/internal/pluggy"
)

// Run with:
//
//	TEST_DATABASE_URL=postgres://... go test -tags=integration ./internal/openfinance/...
//
// against a DISPOSABLE, already-migrated database (never DATABASE_URL): it writes synthetic data
// (no real bank data anywhere in this file) and cleans up after itself.

func setup(t *testing.T) (*pgxpool.Pool, *postgres.Repository) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Like every other integration test here, this assumes the schema is ALREADY applied (CI runs
	// internal/migrations/*.up.sql once before `go test`). Migrating from inside a test would race
	// the other packages, which `go test ./...` runs in parallel against the same database.
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.bank_connections') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("schema missing: apply internal/migrations/*.up.sql to TEST_DATABASE_URL first (err=%v)", err)
	}
	clean := func() {
		for _, q := range []string{
			`DELETE FROM expenses WHERE source IN ('PLUGGY') OR description LIKE 'ITTEST %'`,
			`DELETE FROM income_entries WHERE source = 'PLUGGY'`,
			`DELETE FROM financial_balances WHERE bank_account_id IS NOT NULL`,
			`DELETE FROM bank_connections WHERE item_id LIKE 'ittest-%'`,
		} {
			if _, err := pool.Exec(ctx, q); err != nil {
				t.Logf("cleanup %q: %v", q, err)
			}
		}
	}
	clean()
	t.Cleanup(clean)
	return pool, postgres.NewRepository(pool)
}

func tx(id, acct, date, desc, cat, typ, amount string, billID string) pluggy.Transaction {
	t := pluggy.Transaction{ID: id, AccountID: acct, Date: date + "T12:00:00.000Z", Description: desc, Category: cat,
		Amount: pluggy.Decimal(amount), Type: typ, Status: "POSTED"}
	if billID != "" {
		t.CreditCardMetadata = &pluggy.CreditCardMetadata{BillID: billID}
	}
	return t
}

func snapshot(itemID string, withUber bool) openfinance.Snapshot {
	card := []pluggy.Transaction{
		tx("ittest-c2", "ittest-card", "2026-08-20", "SUPERMERCADO BOM PRECO", "Groceries", "DEBIT", "100.00", "ittest-bill"),
	}
	if withUber {
		card = append([]pluggy.Transaction{tx("ittest-c1", "ittest-card", "2026-08-10", "UBER *TRIP", "Taxi and ride-hailing", "DEBIT", "200.00", "ittest-bill")}, card...)
	}
	return openfinance.Snapshot{
		ItemID: itemID, SyncedAt: time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC), WindowFrom: "2026-07-01",
		Item: pluggy.Item{ID: itemID, Status: "UPDATED", ExecutionStatus: "SUCCESS", Connector: &pluggy.Connector{Name: "Banco Teste"}},
		Accounts: []openfinance.AccountSnapshot{
			{
				Account: pluggy.Account{ID: "ittest-checking", Type: "BANK", Subtype: "CHECKING_ACCOUNT", Name: "Conta corrente", Balance: "1000.00"},
				Transactions: []pluggy.Transaction{
					tx("ittest-b1", "ittest-checking", "2026-09-04", "PAGAMENTO FATURA CARTAO BANCO TESTE", "", "DEBIT", "-300.00", ""),
					tx("ittest-b2", "ittest-checking", "2026-08-25", "PIX ENVIADO CONTA POUPANCA", "Transfer - PIX", "DEBIT", "-500.00", ""),
					tx("ittest-b3", "ittest-checking", "2026-08-05", "SALARIO EMPRESA", "Salary", "CREDIT", "8000.00", ""),
					tx("ittest-b4", "ittest-checking", "2026-08-15", "PADARIA", "Bakery", "DEBIT", "-42.10", ""),
				},
			},
			{
				Account:      pluggy.Account{ID: "ittest-savings", Type: "BANK", Subtype: "SAVINGS_ACCOUNT", Name: "Poupança", Balance: "5000.00"},
				Transactions: []pluggy.Transaction{tx("ittest-s1", "ittest-savings", "2026-08-25", "PIX RECEBIDO CONTA CORRENTE", "Transfer - PIX", "CREDIT", "500.00", "")},
			},
			{
				Account:      pluggy.Account{ID: "ittest-card", Type: "CREDIT", Name: "Cartão", CreditData: &pluggy.CreditData{CreditLimit: "5000", AvailableCreditLimit: "4700"}},
				Bills:        []pluggy.Bill{{ID: "ittest-bill", DueDate: "2026-09-05T00:00:00.000Z", TotalAmount: "300.00"}},
				Transactions: card,
			},
		},
		Investments:        []pluggy.Investment{{ID: "ittest-inv", Name: "CDB Teste", Type: "FIXED_INCOME", Balance: "2500.55", AnnualRate: "12.5"}},
		InvestmentsFetched: true,
	}
}

func count(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func sum(t *testing.T, pool *pgxpool.Pool, q string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), q).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}

func TestSave_ProjectsWithoutDoubleCounting_AndIsIdempotent(t *testing.T) {
	pool, repo := setup(t)
	ctx := context.Background()

	// A manual expense the household already typed for the same purchase: must be LINKED, not duplicated.
	manualID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO expenses (id, amount, expense_date, description, source) VALUES ($1, 42.10, '2026-08-15', 'ITTEST padaria manual', 'MANUAL')`, manualID); err != nil {
		t.Fatal(err)
	}

	res, err := repo.Save(ctx, snapshot("ittest-item", true))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if res.Accounts != 3 || res.Bills != 1 || res.Investments != 1 {
		t.Errorf("unexpected counts: %+v", res)
	}

	// Card purchases (200 + 100) count once; the R$300 bank debit that pays the statement does not;
	// the own-account transfer (out 500 / in 500) counts on neither side; padaria linked to manual.
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY'`); n != 2 {
		t.Errorf("PLUGGY expenses = %d, want 2 (Uber + supermercado)", n)
	}
	if s := sum(t, pool, `SELECT COALESCE(sum(amount),0)::text FROM expenses WHERE source = 'PLUGGY' OR id = '`+manualID.String()+`'`); s != "342.10" {
		t.Errorf("total spending = %s, want 342.10 (200 + 100 + 42.10, no statement payment, no transfer)", s)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE id = $1 AND bank_transaction_id IS NOT NULL AND source = 'MANUAL'`, manualID); n != 1 {
		t.Errorf("manual expense was not linked to the bank transaction")
	}
	if n := count(t, pool, `SELECT count(*) FROM income_entries WHERE source = 'PLUGGY'`); n != 1 {
		t.Errorf("income entries = %d, want 1 (salary only)", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM bank_transactions WHERE projection_kind = 'IGNORED_CARD_PAYMENT' AND external_id = 'ittest-b1'`); n != 1 {
		t.Errorf("statement payment not ignored")
	}
	if n := count(t, pool, `SELECT count(*) FROM bank_transactions WHERE projection_kind = 'IGNORED_TRANSFER' AND external_id IN ('ittest-b2','ittest-s1')`); n != 2 {
		t.Errorf("own-account transfer pair not ignored on both sides")
	}
	// Card purchases count on their purchase date (internal/platform/cardspend).
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND expense_date = original_date AND original_date IN ('2026-08-10','2026-08-20')`); n != 2 {
		t.Errorf("card purchases not dated on their purchase day")
	}

	// Balances: one per BANK account, none for the card.
	if n := count(t, pool, `SELECT count(*) FROM financial_balances WHERE bank_account_id IS NOT NULL`); n != 2 {
		t.Errorf("linked balances = %d, want 2", n)
	}

	// Idempotency: a second identical sync changes nothing.
	if _, err := repo.Save(ctx, snapshot("ittest-item", true)); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY'`); n != 2 {
		t.Errorf("expenses after re-sync = %d, want 2 (no duplicates)", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM income_entries WHERE source = 'PLUGGY'`); n != 1 {
		t.Errorf("income after re-sync = %d, want 1", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM bank_transactions WHERE external_id LIKE 'ittest-%'`); n != 7 {
		t.Errorf("raw transactions = %d, want 7", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM investment_snapshots s JOIN investment_positions p ON p.id = s.position_id WHERE p.external_id = 'ittest-inv'`); n != 1 {
		t.Errorf("same-day investment snapshot must upsert, got %d rows", n)
	}
}

func TestSave_BalanceChangeWritesHistory(t *testing.T) {
	pool, repo := setup(t)
	ctx := context.Background()
	if _, err := repo.Save(ctx, snapshot("ittest-item", true)); err != nil {
		t.Fatal(err)
	}
	next := snapshot("ittest-item", true)
	next.SyncedAt = next.SyncedAt.AddDate(0, 0, 1)
	next.Accounts[0].Account.Balance = "1234.56"
	if _, err := repo.Save(ctx, next); err != nil {
		t.Fatal(err)
	}
	var current string
	if err := pool.QueryRow(ctx, `SELECT current_value::text FROM financial_balances fb JOIN bank_accounts a ON a.id = fb.bank_account_id WHERE a.external_id = 'ittest-checking'`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != "1234.56" {
		t.Errorf("balance = %s, want 1234.56", current)
	}
	if n := count(t, pool, `SELECT count(*) FROM financial_balance_history h JOIN financial_balances fb ON fb.id = h.balance_id JOIN bank_accounts a ON a.id = fb.bank_account_id WHERE a.external_id = 'ittest-checking' AND h.balance = 1000.00`); n != 1 {
		t.Errorf("previous balance 1000.00 not snapshotted into history")
	}
}

func TestSave_DeletingASyncedExpenseIsRemembered(t *testing.T) {
	pool, repo := setup(t)
	ctx := context.Background()
	if _, err := repo.Save(ctx, snapshot("ittest-item", true)); err != nil {
		t.Fatal(err)
	}
	// The household deletes the Uber expense by hand ("don't count this").
	if _, err := pool.Exec(ctx, `DELETE FROM expenses WHERE source = 'PLUGGY' AND description LIKE 'UBER%'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Save(ctx, snapshot("ittest-item", true)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND description LIKE 'UBER%'`); n != 0 {
		t.Errorf("deleted expense came back after re-sync")
	}
	if n := count(t, pool, `SELECT count(*) FROM bank_transactions WHERE external_id = 'ittest-c1' AND projection_kind = 'IGNORED_MANUAL' AND projection_override`); n != 1 {
		t.Errorf("raw row was not marked as manually ignored")
	}
}

func TestSave_RemovedAtSourceRetractsUnreviewedProjection(t *testing.T) {
	pool, repo := setup(t)
	ctx := context.Background()
	if _, err := repo.Save(ctx, snapshot("ittest-item", true)); err != nil {
		t.Fatal(err)
	}
	// Pluggy no longer returns the Uber charge (it was reversed at the source).
	if _, err := repo.Save(ctx, snapshot("ittest-item", false)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND description LIKE 'UBER%'`); n != 0 {
		t.Errorf("expense for a transaction removed at the source must be retracted")
	}
	if n := count(t, pool, `SELECT count(*) FROM bank_transactions WHERE external_id = 'ittest-c1' AND removed_at IS NOT NULL`); n != 1 {
		t.Errorf("raw row must be kept and marked removed")
	}
}

func TestSetProjection_OverrideSurvivesResync(t *testing.T) {
	pool, repo := setup(t)
	ctx := context.Background()
	if _, err := repo.Save(ctx, snapshot("ittest-item", true)); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM bank_transactions WHERE external_id = 'ittest-c2'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetProjection(ctx, id, openfinance.KindIgnoredManual); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND description LIKE 'SUPERMERCADO%'`); n != 0 {
		t.Errorf("ignoring a transaction must retract its expense immediately")
	}
	if _, err := repo.Save(ctx, snapshot("ittest-item", true)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND description LIKE 'SUPERMERCADO%'`); n != 0 {
		t.Errorf("a re-sync overwrote the human override")
	}
	// Handing it back to the classifier restores the expense.
	if err := repo.SetProjection(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND description LIKE 'SUPERMERCADO%'`); n != 1 {
		t.Errorf("AUTO must re-project the transaction")
	}
}

// ---- de-duplication against invoices uploaded as PDF ----------------------------------------

func insertInvoiceDocument(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO documents (id, original_filename, file_path, file_type, origin) VALUES ($1, 'ittest-fatura.pdf', '/tmp/ittest-fatura.pdf', 'PDF', 'INVOICE')`, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM expenses WHERE document_id = $1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM documents WHERE id = $1`, id)
	})
	return id
}

// The invoice line is uploaded FIRST (PDF), the bank sync arrives afterwards: same amount, same day,
// same merchant must not become a second expense.
func TestSync_DoesNotDuplicateAnExpenseFromAnUploadedInvoice(t *testing.T) {
	pool, repo := setup(t)
	ctx := context.Background()
	doc := insertInvoiceDocument(t, pool)

	// What the PDF extraction stored: competence date in expense_date, real purchase date in original_date.
	if _, err := pool.Exec(ctx, `
		INSERT INTO expenses (id, document_id, amount, expense_date, original_date, description, source)
		VALUES (gen_random_uuid(), $1, 200.00, '2026-08-01', '2026-08-10', 'Uber - viagem', 'DOCUMENT'),
		       (gen_random_uuid(), $1, 100.00, '2026-08-01', '2026-08-20', 'Supermercado Bom Preco', 'DOCUMENT')
	`, doc); err != nil {
		t.Fatal(err)
	}

	res, err := repo.Save(ctx, snapshot("ittest-item", true))
	if err != nil {
		t.Fatal(err)
	}
	if res.LinkedExisting < 2 {
		t.Errorf("LinkedExisting = %d, want the 2 card purchases linked to the invoice lines", res.LinkedExisting)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND (description LIKE 'UBER%' OR description LIKE 'SUPERMERCADO%')`); n != 0 {
		t.Errorf("bank sync inserted %d duplicate(s) of purchases already on the uploaded invoice", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE document_id = $1 AND bank_transaction_id IS NOT NULL`, doc); n != 2 {
		t.Errorf("invoice lines linked to their bank transaction = %d, want 2", n)
	}
	// Total spending on those two purchases is counted once.
	if s := sum(t, pool, `SELECT COALESCE(sum(amount),0)::text FROM expenses WHERE description LIKE 'Uber%' OR description LIKE 'UBER%' OR description LIKE 'Supermercado%' OR description LIKE 'SUPERMERCADO%'`); s != "300.00" {
		t.Errorf("Uber + supermercado counted as %s, want 300.00 (once)", s)
	}
}

// The bank sync ran FIRST, the invoice PDF is uploaded afterwards (the order that used to double
// count): confirming the extraction must adopt the synced rows, not insert new ones.
func TestUpload_DoesNotDuplicateWhatTheBankAlreadySynced(t *testing.T) {
	pool, repo := setup(t)
	ctx := context.Background()
	if _, err := repo.Save(ctx, snapshot("ittest-item", true)); err != nil {
		t.Fatal(err)
	}
	doc := insertInvoiceDocument(t, pool)

	exp := expensespostgres.NewRepository(pool)
	desc1, desc2, desc3 := "Uber - viagem", "Supermercado Bom Preco", "Loja que so existe na fatura"
	o1, o2, o3 := civil("2026-08-10"), civil("2026-08-20"), civil("2026-08-22")
	lines := []expenses.Expense{
		{ID: uuid.New(), DocumentID: &doc, Amount: "200.00", ExpenseDate: o1, OriginalDate: &o1, Description: &desc1},
		{ID: uuid.New(), DocumentID: &doc, Amount: "100.00", ExpenseDate: o2, OriginalDate: &o2, Description: &desc2},
		{ID: uuid.New(), DocumentID: &doc, Amount: "55.55", ExpenseDate: o3, OriginalDate: &o3, Description: &desc3},
	}
	if err := exp.ReplaceForDocument(ctx, doc, lines); err != nil {
		t.Fatalf("ReplaceForDocument: %v", err)
	}
	// 2 adopted synced rows + 1 genuinely new invoice line, all listed under the document.
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE document_id = $1`, doc); n != 3 {
		t.Errorf("invoice lists %d lines, want 3", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE amount IN (200.00, 100.00) AND (description LIKE 'UBER%' OR description LIKE 'SUPERMERCADO%' OR description LIKE 'Uber%' OR description LIKE 'Supermercado%')`); n != 2 {
		t.Errorf("the two purchases exist %d times, want exactly 2 rows (one each)", n)
	}
	// Adopted rows keep their bank origin and take the statement line's dates.
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE document_id = $1 AND source = 'PLUGGY' AND expense_date = original_date AND original_date IN ('2026-08-10','2026-08-20')`, doc); n != 2 {
		t.Errorf("adopted rows = %d, want 2 (source stays PLUGGY)", n)
	}

	// Re-confirming the same invoice is idempotent: still no duplicates.
	if err := exp.ReplaceForDocument(ctx, doc, lines); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE document_id = $1`, doc); n != 3 {
		t.Errorf("after re-confirm the invoice lists %d lines, want 3", n)
	}
	// Removing the document's lines must not delete money the bank owns.
	if err := exp.ReplaceForDocument(ctx, doc, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND (description LIKE 'UBER%' OR description LIKE 'SUPERMERCADO%')`); n != 2 {
		t.Errorf("clearing the invoice deleted %d bank-synced rows", 2-n)
	}
}

func civil(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

// cardSnapshot is one card account whose transactions are given verbatim.
func cardSnapshot(itemID string, bills []pluggy.Bill, txs ...pluggy.Transaction) openfinance.Snapshot {
	return openfinance.Snapshot{
		ItemID: itemID, SyncedAt: time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC), WindowFrom: "2026-08-01",
		Item: pluggy.Item{ID: itemID, Status: "UPDATED", ExecutionStatus: "SUCCESS", Connector: &pluggy.Connector{Name: "Banco Teste"}},
		Accounts: []openfinance.AccountSnapshot{{
			Account:      pluggy.Account{ID: "ittest-card", Type: "CREDIT", Name: "Cartão"},
			Bills:        bills,
			Transactions: txs,
		}},
	}
}

func withMeta(t pluggy.Transaction, purchaseDate, card string, inst *int) pluggy.Transaction {
	if t.CreditCardMetadata == nil {
		t.CreditCardMetadata = &pluggy.CreditCardMetadata{}
	}
	t.CreditCardMetadata.PurchaseDate = purchaseDate
	t.CreditCardMetadata.CardNumber = card
	t.CreditCardMetadata.InstallmentNumber = inst
	if inst != nil {
		total := 10
		t.CreditCardMetadata.TotalInstallments = &total
	}
	return t
}

// A connector that stamps every charge with the statement day: the purchase date comes from
// creditCardMetadata.purchaseDate, an installment counts in the month it is billed, and once the
// real date arrives an already-projected expense is re-dated.
func TestSync_CardChargesUsePurchaseDate(t *testing.T) {
	pool, repo := setup(t)
	ctx := context.Background()
	bill := []pluggy.Bill{{ID: "ittest-bill", DueDate: "2026-10-08T00:00:00.000Z", TotalAmount: "424.89"}}
	three := 3

	// First sync: no purchaseDate yet, everything on the statement day.
	if _, err := repo.Save(ctx, cardSnapshot("ittest-item", bill,
		tx("ittest-u1", "ittest-card", "2026-09-01", "Uber UBER *TRIP", "", "DEBIT", "100.00", "ittest-bill"),
	)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND expense_date = '2026-09-01'`); n != 1 {
		t.Fatalf("first sync: expense not projected on the statement day")
	}

	// Second sync: the connector now sends purchaseDate/cardNumber; /bills no longer lists the bill.
	if _, err := repo.Save(ctx, cardSnapshot("ittest-item", nil,
		withMeta(tx("ittest-u1", "ittest-card", "2026-09-01", "Uber UBER *TRIP", "", "DEBIT", "100.00", "ittest-bill"), "2026-08-28T21:10:00.000Z", "XXXX8007", nil),
		withMeta(tx("ittest-cb", "ittest-card", "2026-09-01", "CASASBAHIACOM", "", "DEBIT", "324.89", "ittest-bill"), "2026-06-22T10:00:00.000Z", "2036", &three),
	)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM bank_transactions WHERE external_id = 'ittest-u1' AND transaction_date = '2026-08-28' AND posted_date = '2026-09-01' AND card_last4 = '8007' AND bill_id IS NOT NULL`); n != 1 {
		t.Errorf("raw row not re-dated to the purchase day / posted date, card or bill link lost")
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND expense_date = '2026-08-28' AND original_date = '2026-08-28'`); n != 1 {
		t.Errorf("projected expense not re-dated to the purchase day")
	}
	// Installment 3 of a purchase on 22/06 counts on 22/08.
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND amount = 324.89 AND expense_date = '2026-08-22' AND original_date = '2026-06-22'`); n != 1 {
		t.Errorf("installment not counted in the month it is billed")
	}
}

// Bank rows all dated on the statement day can never meet the 2-day rule against an uploaded
// statement printed with real purchase dates; the billing-cycle rule must still adopt them.
func TestUpload_AdoptsChargesTheBankDatedOnTheStatementDay(t *testing.T) {
	pool, repo := setup(t)
	ctx := context.Background()
	bill := []pluggy.Bill{{ID: "ittest-bill", DueDate: "2026-10-08T00:00:00.000Z", TotalAmount: "120.00"}}
	stamped := func() openfinance.Snapshot {
		return cardSnapshot("ittest-item", bill,
			tx("ittest-s1", "ittest-card", "2026-09-01", "Uber UBER *TRIP HELP.U", "", "DEBIT", "20.00", "ittest-bill"),
			tx("ittest-s2", "ittest-card", "2026-09-01", "SPOTIFY", "", "DEBIT", "100.00", "ittest-bill"),
		)
	}
	if _, err := repo.Save(ctx, stamped()); err != nil {
		t.Fatal(err)
	}
	doc := insertInvoiceDocument(t, pool)
	exp := expensespostgres.NewRepository(pool)
	d1, d2, d3 := civil("2026-08-27"), civil("2026-09-19"), civil("2026-09-20")
	u, sp, other := "Uber UBER *TRIP HELP.US", "EBN*SPOTIFY", "Loja X"
	lines := []expenses.Expense{
		{ID: uuid.New(), DocumentID: &doc, Amount: "20.00", ExpenseDate: d1, OriginalDate: &d1, Description: &u},
		{ID: uuid.New(), DocumentID: &doc, Amount: "100.00", ExpenseDate: d2, OriginalDate: &d2, Description: &sp},
		{ID: uuid.New(), DocumentID: &doc, Amount: "7.00", ExpenseDate: d3, OriginalDate: &d3, Description: &other},
	}
	if err := exp.ReplaceForDocument(ctx, doc, lines); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE document_id = $1 AND source = 'PLUGGY'`, doc); n != 2 {
		t.Errorf("adopted %d bank rows, want 2", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE amount IN (20.00, 100.00, 7.00)`); n != 3 {
		t.Errorf("%d expenses, want 3 (no duplicates)", n)
	}
	// The statement's printed dates win, and a later sync does not move them back.
	if _, err := repo.Save(ctx, stamped()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND expense_date IN ('2026-08-27','2026-09-19')`); n != 2 {
		t.Errorf("adopted rows lost the statement's purchase dates")
	}
}

// The October 2026 statement, in miniature: a USD purchase the feed reports in dollars (with the
// BRL value in amountInAccountCurrency) and one IOF row per international purchase, while the PDF
// prints the BRL value and a single "Repasse de IOF" aggregate. Nothing may be counted twice.
func TestUpload_ForeignCurrencyAndAggregateIOF(t *testing.T) {
	pool, repo := setup(t)
	ctx := context.Background()
	bill := []pluggy.Bill{{ID: "ittest-bill", DueDate: "2026-10-08T00:00:00.000Z", TotalAmount: "105.35"}}
	usd := tx("ittest-fx", "ittest-card", "2026-09-13", "difmarkGibraltarGIB", "", "DEBIT", "16.98", "ittest-bill")
	usd.CurrencyCode, usd.AmountInAccountCurrency = "USD", "91.86"
	if _, err := repo.Save(ctx, cardSnapshot("ittest-item", bill,
		usd,
		tx("ittest-iof1", "ittest-card", "2026-09-13", "IOF INTERNACIONAL - difmarkGibraltarGIB", "", "DEBIT", "3.22", "ittest-bill"),
		tx("ittest-iof2", "ittest-card", "2026-09-16", "IOF INTERNACIONAL - OPENCODESAN FRANCISCOUSA", "", "DEBIT", "10.27", "ittest-bill"),
	)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND amount = 91.86`); n != 1 {
		t.Fatalf("USD charge not stored in BRL")
	}

	doc := insertInvoiceDocument(t, pool)
	exp := expensespostgres.NewRepository(pool)
	d1, d2 := civil("2026-09-13"), civil("2026-09-16")
	fx, iof := "difmarkGibraltarGIB", "Repasse de IOF (compras internacionais)"
	lines := []expenses.Expense{
		{ID: uuid.New(), DocumentID: &doc, Amount: "91.86", ExpenseDate: d1, OriginalDate: &d1, Description: &fx},
		{ID: uuid.New(), DocumentID: &doc, Amount: "13.49", ExpenseDate: d2, OriginalDate: &d2, Description: &iof},
	}
	if err := exp.ReplaceForDocument(ctx, doc, lines); err != nil {
		t.Fatal(err)
	}
	if s := sum(t, pool, `SELECT COALESCE(sum(amount),0)::text FROM expenses`); s != "105.35" {
		t.Errorf("total = %s, want 105.35 (counted once)", s)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE document_id = $1 AND source = 'PLUGGY'`, doc); n != 3 {
		t.Errorf("document adopted %d bank rows, want 3 (charge + 2 IOF)", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'DOCUMENT'`); n != 0 {
		t.Errorf("%d statement lines inserted, want 0 (all adopted)", n)
	}
}

// A charge projected before the amount fix keeps tracking the bank row's (now correct) amount.
func TestSync_ProjectedCardAmountFollowsTheRawRow(t *testing.T) {
	pool, repo := setup(t)
	ctx := context.Background()
	first := tx("ittest-fx", "ittest-card", "2026-09-13", "difmark", "", "DEBIT", "16.98", "")
	if _, err := repo.Save(ctx, cardSnapshot("ittest-item", nil, first)); err != nil {
		t.Fatal(err)
	}
	fixed := first
	fixed.CurrencyCode, fixed.AmountInAccountCurrency = "USD", "91.86"
	if _, err := repo.Save(ctx, cardSnapshot("ittest-item", nil, fixed)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expenses WHERE source = 'PLUGGY' AND amount = 91.86`); n != 1 {
		t.Errorf("projected expense kept the stale amount")
	}
}

// A statement paid in full from the checking account shows as paid; one with no matching
// payment does not.
func TestListBills_PaidAt(t *testing.T) {
	_, repo := setup(t)
	ctx := context.Background()
	if _, err := repo.Save(ctx, snapshot("ittest-item", true)); err != nil {
		t.Fatal(err)
	}
	bills, err := repo.ListBills(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(bills) != 1 || bills[0].PaidAt == nil || bills[0].PaidAt.Format("2006-01-02") != "2026-09-04" {
		t.Fatalf("bill paid from the checking account on 2026-09-04 not reported as paid: %+v", bills)
	}

	unpaid := cardSnapshot("ittest-item2", []pluggy.Bill{{ID: "ittest-bill2", DueDate: "2026-10-08T00:00:00.000Z", TotalAmount: "77.00"}},
		tx("ittest-x1", "ittest-card", "2026-09-20", "LOJA", "", "DEBIT", "77.00", "ittest-bill2"))
	if _, err := repo.Save(ctx, unpaid); err != nil {
		t.Fatal(err)
	}
	bills, err = repo.ListBills(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bills {
		if b.TotalAmount == "77.00" && b.PaidAt != nil {
			t.Errorf("unpaid statement reported as paid on %s", b.PaidAt.Format("2006-01-02"))
		}
	}
}
