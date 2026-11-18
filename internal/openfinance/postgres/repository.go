// Package postgres implements openfinance.Repository — the only file of the module that imports a
// SQL driver. Save is deliberately one database transaction per connection: raw mirror, transfer/
// statement pairing, projection into expenses/income_entries and balance updates commit together
// or not at all, so a failure can never leave a half-projected sync that later double-counts.
//
// It writes to tables owned by other modules (expenses, income_entries, financial_balances,
// categories) directly, inside that same transaction, on purpose: going through those modules'
// services would split the sync across several transactions. The columns it writes are the ones
// migration 000022 added for exactly this (source, bank_transaction_id, bank_account_id).
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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/categorize"
	"github.com/atlas/atlas-go/internal/openfinance"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/cardspend"
	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/platform/textmatch"
	"github.com/atlas/atlas-go/internal/pluggy"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

var _ openfinance.Repository = (*Repository)(nil)

// pairingWindow bounds how far back the cross-account passes look; older rows were settled by
// earlier syncs and are never re-evaluated.
const pairingWindow = 400 * 24 * time.Hour

func parseDate(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", pluggy.DateOf(s))
	return t, err == nil
}

func nullable(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func nullableMoney(d pluggy.Decimal) *string {
	if !d.IsSet() {
		return nil
	}
	s := d.Money()
	return &s
}

func nullablePrecise(d pluggy.Decimal, scale int) *string {
	if !d.IsSet() {
		return nil
	}
	s := d.Precise(scale)
	return &s
}

// ---- connection state ----------------------------------------------------------------------

// RecordItem upserts the connection row with Pluggy's latest view of the item and returns the
// previous status (empty for a new connection) so the caller can notify only on a transition.
func (r *Repository) RecordItem(ctx context.Context, itemID string, item pluggy.Item, syncErr string) (prevStatus string, err error) {
	err = db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		var prev *string
		if e := tx.QueryRow(ctx, `SELECT status FROM bank_connections WHERE item_id = $1`, itemID).Scan(&prev); e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if prev != nil {
			prevStatus = *prev
		}
		return upsertConnection(ctx, tx, itemID, item, syncErr, nil)
	})
	return prevStatus, err
}

func upsertConnection(ctx context.Context, tx pgx.Tx, itemID string, item pluggy.Item, syncErr string, syncedAt *time.Time) error {
	var connector, institution string
	if item.Connector != nil {
		connector, institution = item.Connector.Name, item.Connector.InstitutionName
	}
	var providerUpdated *time.Time
	if t, err := time.Parse(time.RFC3339, item.LastUpdatedAt); err == nil {
		providerUpdated = &t
	}
	lastErr := syncErr
	if lastErr == "" && item.Error != nil {
		lastErr = strings.TrimSpace(item.Error.Code + " " + item.Error.Message)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO bank_connections (item_id, connector_name, institution_name, status, execution_status,
		                              last_error, provider_updated_at, last_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (item_id) DO UPDATE SET
			connector_name      = COALESCE(EXCLUDED.connector_name, bank_connections.connector_name),
			institution_name    = COALESCE(EXCLUDED.institution_name, bank_connections.institution_name),
			status              = COALESCE(EXCLUDED.status, bank_connections.status),
			execution_status    = COALESCE(EXCLUDED.execution_status, bank_connections.execution_status),
			last_error          = EXCLUDED.last_error,
			provider_updated_at = COALESCE(EXCLUDED.provider_updated_at, bank_connections.provider_updated_at),
			last_synced_at      = COALESCE(EXCLUDED.last_synced_at, bank_connections.last_synced_at)
	`, itemID, nullable(connector), nullable(institution), nullable(item.Status), nullable(item.ExecutionStatus),
		nullable(lastErr), providerUpdated, syncedAt)
	return err
}

func (r *Repository) LastSyncedAt(ctx context.Context, itemID string) (*time.Time, error) {
	var t *time.Time
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `SELECT last_synced_at FROM bank_connections WHERE item_id = $1`, itemID).Scan(&t)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		return e
	})
	return t, err
}

// RequestSync flags every connection so the worker syncs on its next tick. It reports whether any
// connection exists (false: nothing has ever synced, the caller can say so).
func (r *Repository) RequestSync(ctx context.Context) (bool, error) {
	var n int64
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `UPDATE bank_connections SET sync_requested_at = now()`)
		n = tag.RowsAffected()
		return e
	})
	return n > 0, err
}

// TakeSyncRequest atomically clears a pending request and reports whether there was one.
func (r *Repository) TakeSyncRequest(ctx context.Context) (bool, error) {
	var n int64
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `UPDATE bank_connections SET sync_requested_at = NULL WHERE sync_requested_at IS NOT NULL`)
		n = tag.RowsAffected()
		return e
	})
	return n > 0, err
}

// ---- Save ----------------------------------------------------------------------------------

func (r *Repository) Save(ctx context.Context, snap openfinance.Snapshot) (openfinance.SaveResult, error) {
	var res openfinance.SaveResult
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if err := upsertConnection(ctx, tx, snap.ItemID, snap.Item, "", &snap.SyncedAt); err != nil {
			return fmt.Errorf("upserting connection: %w", err)
		}
		var connID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM bank_connections WHERE item_id = $1`, snap.ItemID).Scan(&connID); err != nil {
			return fmt.Errorf("reading connection id: %w", err)
		}
		for _, as := range snap.Accounts {
			windowFrom := snap.WindowFrom
			if as.WindowFrom != "" {
				windowFrom = as.WindowFrom
			}
			if err := saveAccount(ctx, tx, connID, windowFrom, as, &res); err != nil {
				return err
			}
		}
		if err := saveInvestments(ctx, tx, connID, snap, &res); err != nil {
			return err
		}
		if err := pairCrossAccount(ctx, tx, snap.SyncedAt, &res); err != nil {
			return err
		}
		if err := projectConnection(ctx, tx, connID, &res); err != nil {
			return err
		}
		return syncBalances(ctx, tx, connID, snap.SyncedAt, &res)
	})
	return res, err
}

func saveAccount(ctx context.Context, tx pgx.Tx, connID uuid.UUID, windowFrom string, as openfinance.AccountSnapshot, res *openfinance.SaveResult) error {
	a := as.Account
	if a.Type != openfinance.AccountTypeBank && a.Type != openfinance.AccountTypeCredit {
		return nil // an account kind this module doesn't model: skip rather than guess
	}
	currency := a.CurrencyCode
	if currency == "" {
		currency = "BRL"
	}
	var creditLimit, available *string
	if a.CreditData != nil {
		creditLimit, available = nullableMoney(a.CreditData.CreditLimit), nullableMoney(a.CreditData.AvailableCreditLimit)
	}
	var accountID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO bank_accounts (connection_id, external_id, account_type, subtype, name, masked_number,
		                           balance, currency, credit_limit, available_credit, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::numeric, $8, $9::numeric, $10::numeric, now())
		ON CONFLICT (external_id) DO UPDATE SET
			connection_id = EXCLUDED.connection_id, account_type = EXCLUDED.account_type,
			subtype = EXCLUDED.subtype, name = EXCLUDED.name, masked_number = EXCLUDED.masked_number,
			balance = EXCLUDED.balance, currency = EXCLUDED.currency, credit_limit = EXCLUDED.credit_limit,
			available_credit = EXCLUDED.available_credit, updated_at = now()
		RETURNING id
	`, connID, a.ID, a.Type, nullable(a.Subtype), nullable(a.Name), nullable(a.Number),
		nullableMoney(a.Balance), currency, creditLimit, available).Scan(&accountID); err != nil {
		return fmt.Errorf("upserting account: %w", err)
	}
	res.Accounts++

	// Statements first: transactions link to them by Pluggy's bill id.
	purchaseDates := map[string][]string{}
	if a.Type == openfinance.AccountTypeCredit {
		for _, t := range as.Transactions {
			if t.CreditCardMetadata != nil && t.CreditCardMetadata.BillID != "" && t.AccountAmount(currency).Sign() > 0 {
				purchaseDates[t.CreditCardMetadata.BillID] = append(purchaseDates[t.CreditCardMetadata.BillID], t.PurchaseDay())
			}
		}
	}
	billIDs := map[string]uuid.UUID{}
	for _, b := range as.Bills {
		due, ok := parseDate(b.DueDate)
		competence, cok := parseDate(openfinance.BillCompetence(b, purchaseDates[b.ID]))
		// Only purchases actually attached to the statement prove its month; the due-date fallback
		// is a guess, so it may seed a new row but never overwrites a month already known (an
		// incremental sync sees only recent purchases and used to flip old statements' months).
		competenceKnown := len(purchaseDates[b.ID]) > 0
		if !ok || !cok {
			continue // a statement with no usable dates can't be placed in a month
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO credit_card_bills (account_id, external_id, due_date, competence, total_amount,
			                               minimum_payment, allows_installments, finance_charges, updated_at)
			VALUES ($1, $2, $3, $4, $5::numeric, $6::numeric, $7, $8::numeric, now())
			ON CONFLICT (external_id) DO UPDATE SET
				due_date = EXCLUDED.due_date,
				competence = CASE WHEN $9 THEN EXCLUDED.competence ELSE credit_card_bills.competence END,
				total_amount = EXCLUDED.total_amount,
				minimum_payment = EXCLUDED.minimum_payment, allows_installments = EXCLUDED.allows_installments,
				finance_charges = EXCLUDED.finance_charges, updated_at = now()
			RETURNING id
		`, accountID, b.ID, due, competence, b.TotalAmount.Money(), nullableMoney(b.MinimumPaymentAmount),
			b.AllowsInstallments, openfinance.BillFinanceCharges(b), competenceKnown).Scan(&id); err != nil {
			return fmt.Errorf("upserting bill: %w", err)
		}
		billIDs[b.ID] = id
		res.Bills++
	}

	if len(as.Transactions) == 0 {
		// An empty answer can't be told apart from an upstream hiccup, so it never retracts anything.
		return nil
	}
	batch := &pgx.Batch{}
	seen := make([]string, 0, len(as.Transactions))
	minDate := ""
	for _, t := range as.Transactions {
		posted, ok := parseDate(t.Date)
		if !ok || t.ID == "" {
			continue
		}
		// A card charge is dated when the card was used. Some connectors stamp Date with the
		// statement/posting day instead (every charge of a bill on the same day); the real day is
		// in creditCardMetadata.purchaseDate. Date is kept as posted_date (the sync window and the
		// removal sweep are expressed in it).
		date := posted
		var cardLast4 *string
		if a.Type == openfinance.AccountTypeCredit {
			if d, ok := parseDate(t.PurchaseDay()); ok {
				date = d
			}
			cardLast4 = nullable(t.CardLast4())
		}
		amount := t.AccountAmount(currency)
		direction := t.Type
		if direction != "DEBIT" && direction != "CREDIT" {
			direction = "DEBIT"
			if (a.Type == openfinance.AccountTypeBank && amount.Sign() > 0) || (a.Type == openfinance.AccountTypeCredit && amount.Sign() < 0) {
				direction = "CREDIT"
			}
		}
		status := t.Status
		if status != "PENDING" {
			status = "POSTED"
		}
		kind := openfinance.Classify(openfinance.ClassifyInput{
			AccountType: a.Type, Direction: direction, Amount: amount, Status: status,
			Description: t.Description, ProviderCategory: t.Category,
		})
		var billID *uuid.UUID
		var instNum, instTotal *int
		if md := t.CreditCardMetadata; md != nil {
			if id, ok := billIDs[md.BillID]; ok {
				billID = &id
			}
			instNum, instTotal = md.InstallmentNumber, md.TotalInstallments
		}
		desc := t.Description
		if len(desc) > 500 {
			desc = desc[:500]
		}
		batch.Queue(`
			INSERT INTO bank_transactions (account_id, bill_id, external_id, transaction_date, amount, direction,
			                               status, description, provider_category, installment_number,
			                               installment_total, projection_kind, posted_date, card_last4)
			VALUES ($1, $2, $3, $4, $5::numeric, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (external_id) DO UPDATE SET
				-- /bills does not always list the statement a charge belongs to (the open one, for
				-- one): a sync that can't resolve it must not erase a link an earlier sync made.
				bill_id = COALESCE(EXCLUDED.bill_id, bank_transactions.bill_id),
				transaction_date = EXCLUDED.transaction_date, amount = EXCLUDED.amount,
				posted_date = EXCLUDED.posted_date, card_last4 = COALESCE(EXCLUDED.card_last4, bank_transactions.card_last4),
				direction = EXCLUDED.direction, status = EXCLUDED.status, description = EXCLUDED.description,
				provider_category = EXCLUDED.provider_category, installment_number = EXCLUDED.installment_number,
				installment_total = EXCLUDED.installment_total,
				projection_kind = CASE WHEN bank_transactions.projection_override
				                       THEN bank_transactions.projection_kind ELSE EXCLUDED.projection_kind END,
				removed_at = NULL, updated_at = now()
		`, accountID, billID, t.ID, date, amount.Money(), direction, status, nullable(desc),
			nullable(t.Category), instNum, instTotal, string(kind), posted, cardLast4)
		seen = append(seen, t.ID)
		if d := pluggy.DateOf(t.Date); minDate == "" || d < minDate {
			minDate = d
		}
		res.Transactions++
	}
	br := tx.SendBatch(ctx, batch)
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("upserting transaction: %w", err)
		}
	}
	if err := br.Close(); err != nil {
		return err
	}
	// Rows inside the fetched window that Pluggy no longer returns were deleted at the source.
	// The window is what was ASKED for, not the span of what came back: a transaction older than
	// the oldest returned one is exactly the kind that disappears.
	if windowFrom == "" {
		windowFrom = minDate
	}
	if from, ok := parseDate(windowFrom); ok {
		if _, err := tx.Exec(ctx, `
			UPDATE bank_transactions SET removed_at = now()
			WHERE account_id = $1 AND COALESCE(posted_date, transaction_date) >= $2 AND removed_at IS NULL AND external_id <> ALL($3)
		`, accountID, from, seen); err != nil {
			return fmt.Errorf("marking removed transactions: %w", err)
		}
	}
	return nil
}

func saveInvestments(ctx context.Context, tx pgx.Tx, connID uuid.UUID, snap openfinance.Snapshot, res *openfinance.SaveResult) error {
	today := time.Date(snap.SyncedAt.Year(), snap.SyncedAt.Month(), snap.SyncedAt.Day(), 0, 0, 0, 0, time.UTC)
	ids := make([]string, 0, len(snap.Investments))
	for _, inv := range snap.Investments {
		if inv.ID == "" {
			continue
		}
		name := inv.Name
		if strings.TrimSpace(name) == "" {
			name = inv.Code
		}
		if strings.TrimSpace(name) == "" {
			name = "Investimento"
		}
		currency := inv.CurrencyCode
		if currency == "" {
			currency = "BRL"
		}
		invType := inv.Type
		if invType == "" {
			invType = "OTHER"
		}
		due, _ := parseDate(inv.DueDate)
		var dueArg *time.Time
		if !due.IsZero() {
			dueArg = &due
		}
		var posID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO investment_positions (connection_id, external_id, name, code, investment_type, subtype, status,
			                                  currency, balance, amount_original, amount_profit, quantity,
			                                  annual_rate, last_month_rate, last_12_months_rate, due_date, last_synced_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::numeric, $10::numeric, $11::numeric, $12::numeric,
			        $13::numeric, $14::numeric, $15::numeric, $16, now())
			ON CONFLICT (external_id) DO UPDATE SET
				connection_id = EXCLUDED.connection_id, name = EXCLUDED.name, code = EXCLUDED.code,
				investment_type = EXCLUDED.investment_type, subtype = EXCLUDED.subtype, status = EXCLUDED.status,
				currency = EXCLUDED.currency, balance = EXCLUDED.balance, amount_original = EXCLUDED.amount_original,
				amount_profit = EXCLUDED.amount_profit, quantity = EXCLUDED.quantity, annual_rate = EXCLUDED.annual_rate,
				last_month_rate = EXCLUDED.last_month_rate, last_12_months_rate = EXCLUDED.last_12_months_rate,
				due_date = EXCLUDED.due_date, last_synced_at = now()
			RETURNING id
		`, connID, inv.ID, name, nullable(inv.Code), invType, nullable(inv.Subtype), nullable(inv.Status), currency,
			inv.Balance.Money(), nullableMoney(inv.AmountOriginal), nullableMoney(inv.AmountProfit),
			nullablePrecise(inv.Quantity, 8), nullablePrecise(inv.AnnualRate, 6), nullablePrecise(inv.LastMonthRate, 6),
			nullablePrecise(inv.LastTwelveMonthsRate, 6), dueArg).Scan(&posID); err != nil {
			return fmt.Errorf("upserting investment: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO investment_snapshots (position_id, recorded_on, balance) VALUES ($1, $2, $3::numeric)
			ON CONFLICT (position_id, recorded_on) DO UPDATE SET balance = EXCLUDED.balance
		`, posID, today, inv.Balance.Money()); err != nil {
			return fmt.Errorf("upserting investment snapshot: %w", err)
		}
		ids = append(ids, inv.ID)
		res.Investments++
	}
	// A position Pluggy stopped returning was redeemed/closed. Only when the fetch itself worked:
	// a failed call must never close everything.
	if snap.InvestmentsFetched {
		if _, err := tx.Exec(ctx, `
			UPDATE investment_positions SET status = 'CLOSED'
			WHERE connection_id = $1 AND external_id <> ALL($2) AND COALESCE(status, '') <> 'CLOSED'
		`, connID, ids); err != nil {
			return fmt.Errorf("closing missing investments: %w", err)
		}
	}
	return nil
}

// pairCrossAccount runs the two passes that need to see across accounts and connections: pairing
// a debit with the matching credit on another own account (a transfer), and matching a bank debit
// to a card statement total (a payment). Both only downgrade rows the classifier called
// EXPENSE/INCOME and that no human overrode.
func pairCrossAccount(ctx context.Context, tx pgx.Tx, now time.Time, res *openfinance.SaveResult) error {
	since := now.Add(-pairingWindow)
	rows, err := tx.Query(ctx, `
		SELECT t.id::text, t.account_id::text, t.transaction_date, abs(t.amount)::text, t.direction,
		       COALESCE(t.description, ''), COALESCE(t.provider_category, ''), t.projection_kind
		FROM bank_transactions t JOIN bank_accounts a ON a.id = t.account_id
		WHERE a.account_type = 'BANK' AND t.status = 'POSTED' AND t.removed_at IS NULL
		  AND NOT t.projection_override AND t.projection_kind IN ('EXPENSE', 'INCOME')
		  AND t.transaction_date >= $1
		  -- Never reclassify a row the household already classified/reviewed: a later sync must not
		  -- overwrite a human's decision by downgrading it to a transfer or statement payment.
		  AND NOT EXISTS (SELECT 1 FROM expenses e
		                    WHERE e.bank_transaction_id = t.id AND (e.reviewed OR e.category_source = 'MANUAL'))
		  AND NOT EXISTS (SELECT 1 FROM income_entries i
		                    WHERE i.bank_transaction_id = t.id AND (i.reviewed OR i.category_source = 'MANUAL'))
	`, since)
	if err != nil {
		return fmt.Errorf("loading pairing candidates: %w", err)
	}
	var transfers, debits []openfinance.Candidate
	for rows.Next() {
		var c openfinance.Candidate
		var amount, desc, cat, kind string
		if err := rows.Scan(&c.ID, &c.AccountID, &c.Date, &amount, &c.Direction, &desc, &cat, &kind); err != nil {
			rows.Close()
			return err
		}
		c.Amount, _ = new(big.Rat).SetString(amount)
		if c.Amount == nil {
			continue
		}
		if openfinance.IsTransferLike(desc, cat) {
			transfers = append(transfers, c)
		}
		if c.Direction == "DEBIT" {
			debits = append(debits, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Statement payments first: a debit that settles a statement is a card payment even if it also
	// looks transfer-like ("PIX ... FATURA").
	billRows, err := tx.Query(ctx, `SELECT due_date, total_amount::text FROM credit_card_bills WHERE due_date >= $1`, since)
	if err != nil {
		return fmt.Errorf("loading statements: %w", err)
	}
	var bills []openfinance.BillPayment
	for billRows.Next() {
		var due time.Time
		var total string
		if err := billRows.Scan(&due, &total); err != nil {
			billRows.Close()
			return err
		}
		if t, ok := new(big.Rat).SetString(total); ok {
			bills = append(bills, openfinance.BillPayment{Due: due, Total: t})
		}
	}
	billRows.Close()
	if err := billRows.Err(); err != nil {
		return err
	}
	paid := openfinance.MatchBillPayments(debits, bills)
	if len(paid) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE bank_transactions SET projection_kind = 'IGNORED_CARD_PAYMENT', updated_at = now() WHERE id = ANY($1::uuid[])`, paid); err != nil {
			return fmt.Errorf("marking statement payments: %w", err)
		}
	}
	paidSet := map[string]bool{}
	for _, id := range paid {
		paidSet[id] = true
	}
	var remaining []openfinance.Candidate
	for _, c := range transfers {
		if !paidSet[c.ID] {
			remaining = append(remaining, c)
		}
	}
	pairs := openfinance.PairTransfers(remaining)
	if len(pairs) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE bank_transactions SET projection_kind = 'IGNORED_TRANSFER', updated_at = now() WHERE id = ANY($1::uuid[])`, pairs); err != nil {
			return fmt.Errorf("marking transfers: %w", err)
		}
		res.IgnoredTransfers += len(pairs)
	}
	return nil
}

// ---- projection ----------------------------------------------------------------------------

type categoryCache struct {
	tx   pgx.Tx
	ids  map[string]uuid.UUID // "type|lower(name)" -> id
	load bool
}

func (c *categoryCache) resolve(ctx context.Context, name, kind string) (*uuid.UUID, error) {
	if name == "" {
		return nil, nil
	}
	if !c.load {
		rows, err := c.tx.Query(ctx, `SELECT id, lower(name), COALESCE(type, '') FROM categories`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id uuid.UUID
			var n, t string
			if err := rows.Scan(&id, &n, &t); err != nil {
				rows.Close()
				return nil, err
			}
			c.ids[t+"|"+n] = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		c.load = true
	}
	key := kind + "|" + strings.ToLower(name)
	if id, ok := c.ids[key]; ok {
		return &id, nil
	}
	id := uuid.New()
	if _, err := c.tx.Exec(ctx, `INSERT INTO categories (id, name, type) VALUES ($1, $2, $3)`, id, name, kind); err != nil {
		return nil, err
	}
	c.ids[key] = id
	return &id, nil
}

// loadCategoryMemory builds the category memory from one projected table's already-classified rows.
// table is one of the fixed literals projectConnection passes, never user input.
func loadCategoryMemory(ctx context.Context, tx pgx.Tx, table string) (categorize.Memory, error) {
	rows, err := tx.Query(ctx, fmt.Sprintf(`
		SELECT COALESCE(e.description, ''), c.name, e.category_source = 'MANUAL'
		FROM %s e
		JOIN categories c ON c.id = e.category_id
		WHERE e.category_id IS NOT NULL AND COALESCE(e.description, '') <> ''
	`, table))
	if err != nil {
		return categorize.Memory{}, fmt.Errorf("loading %s category history: %w", table, err)
	}
	defer rows.Close()
	var examples []categorize.Example
	for rows.Next() {
		var example categorize.Example
		if err := rows.Scan(&example.Description, &example.Category, &example.Manual); err != nil {
			return categorize.Memory{}, fmt.Errorf("scanning %s category history: %w", table, err)
		}
		examples = append(examples, example)
	}
	if err := rows.Err(); err != nil {
		return categorize.Memory{}, err
	}
	return categorize.NewMemory(examples), nil
}

// autoCategory decides a launch's category, preferring the household's own classification: a
// strong memory match first, then the keyword rule. A weak match is deliberately NOT applied — it
// is low confidence and the row is left uncategorised for the manual-review modal instead. The
// second return value is the provenance recorded in category_source.
func autoCategory(memory categorize.Memory, ruleCategory, description string) (string, string) {
	suggestion, ok := memory.Suggest(description)
	if ok && suggestion.Strong {
		return suggestion.Category, "HISTORY"
	}
	if ruleCategory != "" {
		return ruleCategory, "RULE"
	}
	return "", "NONE"
}

// projectConnection makes expenses/income_entries match the classified raw rows of one connection:
// stale projections are removed (unless a human already reviewed them), an existing manual or PDF
// row with the same amount and date is LINKED instead of duplicated, and everything else is
// inserted. Card charges count on their purchase date (installment n in the month it is billed —
// internal/platform/cardspend), the same rule the PDF invoice flow uses; original_date keeps the
// purchase date. Already-projected card charges are re-dated when the raw row's date changes (a
// connector fix, or purchaseDate arriving), unless an uploaded statement owns the line.
func projectConnection(ctx context.Context, tx pgx.Tx, connID uuid.UUID, res *openfinance.SaveResult) error {
	// 1. Retract projections whose raw row is no longer spending/income.
	for _, tbl := range []struct{ table, kind string }{{"expenses", "EXPENSE"}, {"income_entries", "INCOME"}} {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`
			DELETE FROM %[1]s x USING bank_transactions t, bank_accounts a
			WHERE x.bank_transaction_id = t.id AND a.id = t.account_id AND a.connection_id = $1
			  AND x.source = 'PLUGGY' AND NOT x.reviewed AND x.category_source <> 'MANUAL'
			  AND (t.projection_kind <> $2 OR t.removed_at IS NOT NULL)
		`, tbl.table), connID, tbl.kind); err != nil {
			return fmt.Errorf("retracting stale %s: %w", tbl.table, err)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`
			UPDATE %[1]s x SET bank_transaction_id = NULL FROM bank_transactions t, bank_accounts a
			WHERE x.bank_transaction_id = t.id AND a.id = t.account_id AND a.connection_id = $1
			  AND (t.projection_kind <> $2 OR t.removed_at IS NOT NULL)
		`, tbl.table), connID, tbl.kind); err != nil {
			return fmt.Errorf("unlinking %s: %w", tbl.table, err)
		}
	}

	// 2. Keep projected card charges in step with their raw row: the amount (the bank owns it — a
	// foreign-currency charge used to be stored in the original currency) and, unless an uploaded
	// statement owns the line and its printed dates, the dates.
	if _, err := tx.Exec(ctx, `
		UPDATE expenses x SET amount = t.amount
		FROM bank_transactions t, bank_accounts a
		WHERE x.bank_transaction_id = t.id AND a.id = t.account_id AND a.connection_id = $1
		  AND a.account_type = 'CREDIT' AND x.source = 'PLUGGY' AND x.amount <> t.amount
	`, connID); err != nil {
		return fmt.Errorf("updating card expense amounts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE expenses x
		SET expense_date = (t.transaction_date + make_interval(months => GREATEST(COALESCE(t.installment_number, 1) - 1, 0)))::date,
		    original_date = t.transaction_date
		FROM bank_transactions t, bank_accounts a
		WHERE x.bank_transaction_id = t.id AND a.id = t.account_id AND a.connection_id = $1
		  AND a.account_type = 'CREDIT' AND x.source = 'PLUGGY' AND x.document_id IS NULL
		  AND (x.original_date IS DISTINCT FROM t.transaction_date
		       OR x.expense_date <> (t.transaction_date + make_interval(months => GREATEST(COALESCE(t.installment_number, 1) - 1, 0)))::date)
	`, connID); err != nil {
		return fmt.Errorf("re-dating card expenses: %w", err)
	}

	// 3. Project what is missing.
	rows, err := tx.Query(ctx, `
		SELECT t.id, t.transaction_date, t.amount::text, COALESCE(t.description, ''), COALESCE(t.provider_category, ''),
		       t.installment_number, t.installment_total, a.account_type, t.projection_kind
		FROM bank_transactions t
		JOIN bank_accounts a ON a.id = t.account_id
		WHERE a.connection_id = $1 AND t.removed_at IS NULL AND t.projection_kind IN ('EXPENSE', 'INCOME')
		  AND NOT EXISTS (SELECT 1 FROM expenses e WHERE e.bank_transaction_id = t.id)
		  AND NOT EXISTS (SELECT 1 FROM income_entries i WHERE i.bank_transaction_id = t.id)
		ORDER BY t.transaction_date, t.id
	`, connID)
	if err != nil {
		return fmt.Errorf("loading unprojected transactions: %w", err)
	}
	type pending struct {
		id                      uuid.UUID
		date                    time.Time
		amount, desc, cat, kind string
		instNum, instTotal      *int
		accountType             string
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.date, &p.amount, &p.desc, &p.cat, &p.instNum, &p.instTotal, &p.accountType, &p.kind); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(todo) == 0 {
		return nil
	}

	cats := &categoryCache{tx: tx, ids: map[string]uuid.UUID{}}
	// The household's own classification beats the keyword rules: a merchant already filed under a
	// category (especially one a human set) is filed the same way here, so a sync months later
	// doesn't split "Uber" between Transporte and Outros (internal/categorize).
	expenseMemory, err := loadCategoryMemory(ctx, tx, "expenses")
	if err != nil {
		return err
	}
	incomeMemory, err := loadCategoryMemory(ctx, tx, "income_entries")
	if err != nil {
		return err
	}
	for _, p := range todo {
		amount, ok := new(big.Rat).SetString(p.amount)
		if !ok {
			continue
		}
		description := p.desc
		if p.instNum != nil && p.instTotal != nil && *p.instTotal > 1 {
			description = fmt.Sprintf("%s (%d/%d)", description, *p.instNum, *p.instTotal)
		}
		if len(description) > 300 {
			description = description[:300]
		}
		date := p.date
		var original *time.Time
		if p.accountType == openfinance.AccountTypeCredit {
			original = &p.date
			if p.instNum != nil {
				date = cardspend.SpendDate(p.date, *p.instNum)
			}
		}

		if p.kind == string(openfinance.KindExpense) {
			// Card: the sign is the raw one (a refund stays negative). Account: money out is positive.
			exp := amount
			if p.accountType == openfinance.AccountTypeBank {
				exp = new(big.Rat).Abs(amount)
			}
			expStr := exp.FloatString(2)
			// The same purchase may already be here (typed by hand, or from an uploaded invoice):
			// link it instead of duplicating. Invoice lines only ever describe CARD purchases.
			linked, err := linkExisting(ctx, tx, "expenses", "expense_date", p.id, expStr, p.date, p.desc, p.accountType == openfinance.AccountTypeCredit)
			if err != nil {
				return fmt.Errorf("linking existing expense: %w", err)
			}
			if linked {
				res.LinkedExisting++
				continue
			}
			name, categorySource := "", "NONE"
			if exp.Sign() < 0 {
				// A refund is an adjustment, not spending — the keyword rule already says so.
				name, categorySource = openfinance.CatAdjustments, "RULE"
			} else {
				name, categorySource = autoCategory(expenseMemory, openfinance.MapExpenseCategory(p.cat, p.desc), description)
			}
			catID, err := cats.resolve(ctx, name, "EXPENSE")
			if err != nil {
				return fmt.Errorf("resolving expense category: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO expenses (id, category_id, amount, expense_date, description, reviewed, original_date,
				                      source, bank_transaction_id, category_source)
				VALUES ($1, $2, $3::numeric, $4, $5, false, $6, 'PLUGGY', $7, $8)
			`, uuid.New(), catID, expStr, date, nullable(description), original, p.id, categorySource); err != nil {
				return fmt.Errorf("inserting expense: %w", err)
			}
			res.ExpensesWritten++
			continue
		}

		// INCOME (bank credits only).
		incStr := new(big.Rat).Abs(amount).FloatString(2)
		linked, err := linkExisting(ctx, tx, "income_entries", "income_date", p.id, incStr, p.date, p.desc, false)
		if err != nil {
			return fmt.Errorf("linking existing income: %w", err)
		}
		if linked {
			res.LinkedExisting++
			continue
		}
		name, categorySource := autoCategory(incomeMemory, openfinance.MapIncomeCategory(p.cat, p.desc), description)
		catID, err := cats.resolve(ctx, name, "INCOME")
		if err != nil {
			return fmt.Errorf("resolving income category: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO income_entries (id, category_id, amount, income_date, description, reviewed, source, bank_transaction_id, category_source)
			VALUES ($1, $2, $3::numeric, $4, $5, false, 'PLUGGY', $6, $7)
		`, uuid.New(), catID, incStr, date, nullable(description), p.id, categorySource); err != nil {
			return fmt.Errorf("inserting income: %w", err)
		}
		res.IncomeWritten++
	}
	return nil
}

// linkExisting looks for an already-stored, still-unlinked row that is the same money as the bank
// transaction and, if found, links it to the raw row instead of letting a second copy be inserted.
// Manual rows can match anything; invoice (DOCUMENT) rows only card charges. A row is consumed by at
// most one bank transaction, so two identical purchases match two rows one-to-one.
//
// Two rules, in order:
//  1. strict (textmatch.Pick): same amount, same day give or take two, same merchant;
//  2. same card statement (textmatch.PickSameStatement), card rows only: an uploaded statement's line
//     whose billing cycle (the span of purchase dates printed on that document, ±3 days) contains
//     the charge's date. It catches charges a connector dated on the statement day instead of the
//     purchase day, which the ±2-day rule alone misses — the purchase would be counted twice.
func linkExisting(ctx context.Context, tx pgx.Tx, table, dateCol string, txID uuid.UUID, amount string, date time.Time, description string, allowDocument bool) (bool, error) {
	cycle := "false"
	if table == "expenses" {
		cycle = `($3 AND x.source = 'DOCUMENT' AND EXISTS (
		            SELECT 1 FROM expenses s
		            WHERE s.document_id = x.document_id AND s.original_date = s.expense_date
		            GROUP BY s.document_id
		            HAVING $2::date BETWEEN min(s.original_date) - 3 AND max(s.original_date) + 3))`
	}
	rows, err := tx.Query(ctx, fmt.Sprintf(`
		SELECT x.id::text, COALESCE(x.original_date, x.%[2]s), COALESCE(x.description, ''), %[3]s
		FROM %[1]s x
		WHERE x.bank_transaction_id IS NULL AND (x.source = 'MANUAL' OR ($3 AND x.source = 'DOCUMENT'))
		  AND x.amount = $1::numeric
		  AND (COALESCE(x.original_date, x.%[2]s) BETWEEN $2::date - 2 AND $2::date + 2 OR %[3]s)
		ORDER BY x.created_at
		FOR UPDATE OF x SKIP LOCKED
	`, table, dateCol, cycle), amount, date, allowDocument)
	if err != nil {
		return false, err
	}
	var strict, sameStatement []textmatch.Candidate
	for rows.Next() {
		var id, desc string
		var purchase time.Time
		var inCycle bool
		if err := rows.Scan(&id, &purchase, &desc, &inCycle); err != nil {
			rows.Close()
			return false, err
		}
		strict = append(strict, textmatch.Candidate{ID: id, Date: purchase, Description: desc})
		if inCycle {
			sameStatement = append(sameStatement, textmatch.Candidate{ID: id, Description: desc})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if i, ok := textmatch.Pick(strict, date, description); ok {
		return linkRow(ctx, tx, table, txID, strict[i].ID)
	}
	if i, ok := textmatch.PickSameStatement(sameStatement, description); ok {
		return linkRow(ctx, tx, table, txID, sameStatement[i].ID)
	}
	return false, nil
}

func linkRow(ctx context.Context, tx pgx.Tx, table string, txID uuid.UUID, rowID string) (bool, error) {
	if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET bank_transaction_id = $1 WHERE id = $2::uuid`, table), txID, rowID); err != nil {
		return false, err
	}
	return true, nil
}

// ---- balances ------------------------------------------------------------------------------

// syncBalances keeps one financial_balances row per BANK account, updated in place. A change
// snapshots the previous value into financial_balance_history first — the same rule
// finance.Service.Update applies to manual edits, so the history stays one continuous timeline.
// Card accounts are liabilities, not balances, and are read from bank_accounts instead.
func syncBalances(ctx context.Context, tx pgx.Tx, connID uuid.UUID, now time.Time, res *openfinance.SaveResult) error {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	rows, err := tx.Query(ctx, `
		SELECT a.id, COALESCE(c.institution_name, c.connector_name, ''), COALESCE(a.name, ''), COALESCE(a.subtype, ''), a.balance::text
		FROM bank_accounts a JOIN bank_connections c ON c.id = a.connection_id
		WHERE a.connection_id = $1 AND a.account_type = 'BANK' AND a.balance IS NOT NULL
	`, connID)
	if err != nil {
		return fmt.Errorf("loading accounts for balances: %w", err)
	}
	type acct struct {
		id                                  uuid.UUID
		institution, name, subtype, balance string
	}
	var accts []acct
	for rows.Next() {
		var a acct
		if err := rows.Scan(&a.id, &a.institution, &a.name, &a.subtype, &a.balance); err != nil {
			rows.Close()
			return err
		}
		accts = append(accts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, a := range accts {
		var balID uuid.UUID
		var current string
		var updatedOn time.Time
		e := tx.QueryRow(ctx, `SELECT id, current_value::text, updated_on FROM financial_balances WHERE bank_account_id = $1`, a.id).Scan(&balID, &current, &updatedOn)
		if errors.Is(e, pgx.ErrNoRows) {
			name := strings.TrimSpace(strings.Join(nonEmpty(a.institution, a.name), " — "))
			if name == "" {
				name = "Conta bancária"
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO financial_balances (id, name, balance_type, current_value, updated_on, notes, bank_account_id)
				VALUES ($1, $2, $3, $4::numeric, $5, 'Sincronizado automaticamente via Open Finance', $6)
			`, uuid.New(), name, openfinance.BalanceTypeFor(a.subtype), a.balance, today, a.id); err != nil {
				return fmt.Errorf("creating linked balance: %w", err)
			}
			res.BalancesUpdated++
			continue
		}
		if e != nil {
			return e
		}
		newV, ok1 := new(big.Rat).SetString(a.balance)
		oldV, ok2 := new(big.Rat).SetString(current)
		if ok1 && ok2 && newV.Cmp(oldV) == 0 && updatedOn.Equal(today) {
			continue
		}
		if !(ok1 && ok2 && newV.Cmp(oldV) == 0) {
			if _, err := tx.Exec(ctx, `
				INSERT INTO financial_balance_history (id, balance_id, balance, recorded_on) VALUES ($1, $2, $3::numeric, $4)
			`, uuid.New(), balID, current, updatedOn); err != nil {
				return fmt.Errorf("snapshotting balance history: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE financial_balances SET current_value = $2::numeric, updated_on = $3 WHERE id = $1`, balID, a.balance, today); err != nil {
			return fmt.Errorf("updating linked balance: %w", err)
		}
		res.BalancesUpdated++
	}
	return nil
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// ---- reads and overrides -------------------------------------------------------------------

func (r *Repository) ListConnections(ctx context.Context) ([]openfinance.Connection, error) {
	var out []openfinance.Connection
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT c.id, c.item_id, c.connector_name, c.institution_name, c.status, c.execution_status, c.last_error,
			       c.provider_updated_at, c.last_synced_at, c.sync_requested_at,
			       (SELECT count(*) FROM bank_accounts a WHERE a.connection_id = c.id)
			FROM bank_connections c ORDER BY c.created_at
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c openfinance.Connection
			if err := rows.Scan(&c.ID, &c.ItemID, &c.ConnectorName, &c.InstitutionName, &c.Status, &c.ExecutionStatus,
				&c.LastError, &c.ProviderUpdatedAt, &c.LastSyncedAt, &c.SyncRequestedAt, &c.AccountCount); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

func (r *Repository) ListAccounts(ctx context.Context) ([]openfinance.AccountView, error) {
	var out []openfinance.AccountView
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT a.id, COALESCE(c.institution_name, c.connector_name), a.account_type, a.subtype, a.name, a.masked_number,
			       a.balance::text, a.currency, a.credit_limit::text, a.available_credit::text, a.updated_at
			FROM bank_accounts a JOIN bank_connections c ON c.id = a.connection_id
			ORDER BY c.created_at, a.account_type, a.name
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v openfinance.AccountView
			if err := rows.Scan(&v.ID, &v.Institution, &v.Type, &v.Subtype, &v.Name, &v.MaskedNumber, &v.Balance,
				&v.Currency, &v.CreditLimit, &v.AvailableCredit, &v.UpdatedAt); err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}

func (r *Repository) ListBills(ctx context.Context, limit int) ([]openfinance.BillView, error) {
	var out []openfinance.BillView
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT b.id, a.id, COALESCE(c.institution_name, c.connector_name), a.name, b.due_date, b.competence,
			       b.total_amount::text, b.minimum_payment::text, b.finance_charges::text,
			       (SELECT count(*) FROM bank_transactions t WHERE t.bill_id = b.id AND t.removed_at IS NULL),
			       CASE WHEN b.total_amount <= 0 THEN b.due_date ELSE (
			           SELECT min(p.transaction_date) FROM bank_transactions p
			           JOIN bank_accounts pa ON pa.id = p.account_id
			           WHERE p.removed_at IS NULL AND p.projection_kind = 'IGNORED_CARD_PAYMENT'
			             AND abs(p.amount) = b.total_amount
			             AND p.transaction_date BETWEEN b.due_date - 30 AND b.due_date + 5
			             AND (p.account_id = b.account_id OR pa.account_type = 'BANK')
			       ) END
			FROM credit_card_bills b
			JOIN bank_accounts a ON a.id = b.account_id
			JOIN bank_connections c ON c.id = a.connection_id
			ORDER BY b.due_date DESC LIMIT $1
		`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v openfinance.BillView
			if err := rows.Scan(&v.ID, &v.AccountID, &v.Institution, &v.AccountName, &v.DueDate, &v.Competence,
				&v.TotalAmount, &v.MinimumPayment, &v.FinanceCharges, &v.TransactionCount, &v.PaidAt); err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}

func (r *Repository) ListTransactions(ctx context.Context, f openfinance.TransactionFilter) ([]openfinance.TransactionView, error) {
	var out []openfinance.TransactionView
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT t.id, a.id, a.account_type, t.transaction_date, t.amount::text, t.direction, t.status,
			       COALESCE(t.description, ''), t.provider_category, t.projection_kind, t.projection_override
			FROM bank_transactions t JOIN bank_accounts a ON a.id = t.account_id
			WHERE t.removed_at IS NULL
			  AND ($1 = '' OR t.projection_kind = $1)
			  AND ($2 = '' OR t.projection_kind LIKE 'IGNORED%')
			ORDER BY t.transaction_date DESC, t.id LIMIT $3 OFFSET $4
		`, string(f.Kind), boolToFlag(f.OnlyIgnored), f.Limit, f.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v openfinance.TransactionView
			var kind string
			if err := rows.Scan(&v.ID, &v.AccountID, &v.AccountType, &v.Date, &v.Amount, &v.Direction, &v.Status,
				&v.Description, &v.ProviderCategory, &kind, &v.Overridden); err != nil {
				return err
			}
			v.Kind = openfinance.ProjectionKind(kind)
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}

func boolToFlag(b bool) string {
	if b {
		return "x"
	}
	return ""
}

// SetProjection applies a human decision to one transaction and re-projects its connection in the
// same transaction. kind == "" clears the override and restores the classifier's own verdict.
func (r *Repository) SetProjection(ctx context.Context, id uuid.UUID, kind openfinance.ProjectionKind) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		var connID uuid.UUID
		var accountType, direction, status, desc, cat, amount string
		err := tx.QueryRow(ctx, `
			SELECT a.connection_id, a.account_type, t.direction, t.status, COALESCE(t.description, ''),
			       COALESCE(t.provider_category, ''), t.amount::text
			FROM bank_transactions t JOIN bank_accounts a ON a.id = t.account_id WHERE t.id = $1
		`, id).Scan(&connID, &accountType, &direction, &status, &desc, &cat, &amount)
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.NotFound("transaction not found")
		}
		if err != nil {
			return err
		}
		if kind == "" {
			auto := openfinance.Classify(openfinance.ClassifyInput{
				AccountType: accountType, Direction: direction, Amount: pluggy.Decimal(amount), Status: status,
				Description: desc, ProviderCategory: cat,
			})
			_, err = tx.Exec(ctx, `UPDATE bank_transactions SET projection_kind = $2, projection_override = false, updated_at = now() WHERE id = $1`, id, string(auto))
		} else {
			_, err = tx.Exec(ctx, `UPDATE bank_transactions SET projection_kind = $2, projection_override = true, updated_at = now() WHERE id = $1`, id, string(kind))
		}
		if err != nil {
			return err
		}
		var res openfinance.SaveResult
		return projectConnection(ctx, tx, connID, &res)
	})
}

// EnsureConnections registers a row per configured item and flags a sync, so the very first sync
// runs on the worker's next tick even though nothing has been fetched yet.
func (r *Repository) EnsureConnections(ctx context.Context, itemIDs []string) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		for _, id := range itemIDs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO bank_connections (item_id, sync_requested_at) VALUES ($1, now())
				ON CONFLICT (item_id) DO UPDATE SET sync_requested_at = now()
			`, id); err != nil {
				return fmt.Errorf("registering connection: %w", err)
			}
		}
		return nil
	})
}

func (r *Repository) ListInvestments(ctx context.Context) ([]openfinance.InvestmentView, error) {
	var out []openfinance.InvestmentView
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.id, COALESCE(c.institution_name, c.connector_name), p.name, p.code, p.investment_type, p.subtype,
			       p.status, p.currency, p.balance::text, p.amount_original::text, p.amount_profit::text, p.quantity::text,
			       p.annual_rate::text, p.last_month_rate::text, p.last_12_months_rate::text, p.due_date, p.last_synced_at
			FROM investment_positions p JOIN bank_connections c ON c.id = p.connection_id
			WHERE COALESCE(p.status, '') <> 'CLOSED'
			ORDER BY p.balance DESC, p.name
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v openfinance.InvestmentView
			if err := rows.Scan(&v.ID, &v.Institution, &v.Name, &v.Code, &v.Type, &v.Subtype, &v.Status, &v.Currency,
				&v.Balance, &v.AmountOriginal, &v.AmountProfit, &v.Quantity, &v.AnnualRate, &v.LastMonthRate,
				&v.Last12MonthsRate, &v.DueDate, &v.LastSyncedAt); err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}

func (r *Repository) InvestmentHistory(ctx context.Context, id uuid.UUID) ([]openfinance.InvestmentPoint, error) {
	var out []openfinance.InvestmentPoint
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT recorded_on, balance::text FROM investment_snapshots WHERE position_id = $1 ORDER BY recorded_on`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p openfinance.InvestmentPoint
			if err := rows.Scan(&p.Date, &p.Balance); err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}
