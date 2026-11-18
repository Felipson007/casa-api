// Package openfinance keeps the household's bank data in sync with Pluggy (Open Finance) and
// projects it into the tables the Finanças screens already read: card charges and account debits
// become expenses, account credits become income, account balances become financial balances, and
// investment positions get their own read model.
//
// Two layers, on purpose (see migration 000022):
//
//   - raw rows (bank_accounts, bank_transactions, credit_card_bills, ...) mirror Pluggy
//     idempotently by external id, so a wrong classification rule can be fixed and re-applied
//     without re-fetching, and nothing the bank said is ever lost;
//   - projected rows (expenses, income_entries, financial_balances) carry a link back to the raw
//     row — never a second copy — so syncing twice cannot double-count.
//
// The rules that decide what counts as spending (classify.go) are pure functions with no I/O:
// the biggest correctness risk of this feature is counting a card purchase AND the payment of that
// card's statement (or a transfer between the household's own accounts) as two expenses.
package openfinance

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/pluggy"
)

// ProjectionKind is what a raw bank transaction becomes in Finanças.
type ProjectionKind string

const (
	KindPending            ProjectionKind = "PENDING" // not posted yet: stored, not counted
	KindExpense            ProjectionKind = "EXPENSE"
	KindIncome             ProjectionKind = "INCOME"
	KindIgnoredCardPayment ProjectionKind = "IGNORED_CARD_PAYMENT" // paying a statement: the purchases already count
	KindIgnoredTransfer    ProjectionKind = "IGNORED_TRANSFER"     // between the household's own accounts
	KindIgnoredInvestment  ProjectionKind = "IGNORED_INVESTMENT"   // applying/redeeming an own investment
	KindIgnoredManual      ProjectionKind = "IGNORED_MANUAL"       // the household said "don't count this"
)

// IsIgnored reports whether k is one of the "not spending / not income" kinds.
func (k ProjectionKind) IsIgnored() bool {
	switch k {
	case KindIgnoredCardPayment, KindIgnoredTransfer, KindIgnoredInvestment, KindIgnoredManual:
		return true
	}
	return false
}

// Valid reports whether k is a known kind.
func (k ProjectionKind) Valid() bool {
	return k == KindPending || k == KindExpense || k == KindIncome || k.IsIgnored()
}

// AccountType values as Pluggy names them.
const (
	AccountTypeBank   = "BANK"
	AccountTypeCredit = "CREDIT"
)

// Snapshot is everything fetched for one connection in one sync, in memory, before a single
// database transaction persists it: the network can fail half-way without leaving half a sync in
// the tables.
type Snapshot struct {
	Item     pluggy.Item
	ItemID   string
	SyncedAt time.Time
	// WindowFrom is the first day (YYYY-MM-DD) of the transaction window that was fetched. A
	// transaction inside the window that Pluggy no longer returns was deleted at the source.
	WindowFrom  string
	Accounts    []AccountSnapshot
	Investments []pluggy.Investment
	// InvestmentsFetched is true only when the investments call itself succeeded: positions absent
	// from a successful answer are closed, but a failed call must never close everything.
	InvestmentsFetched bool
}

// AccountSnapshot is one account with its statements and movements.
type AccountSnapshot struct {
	Account      pluggy.Account
	Bills        []pluggy.Bill
	Transactions []pluggy.Transaction
	// WindowFrom overrides Snapshot.WindowFrom for this account (card accounts read further back).
	WindowFrom string
}

// SaveResult summarises what one Save wrote — logged by the worker, returned by the manual sync.
type SaveResult struct {
	Accounts         int `json:"accounts"`
	Transactions     int `json:"transactions"`
	Bills            int `json:"bills"`
	Investments      int `json:"investments"`
	ExpensesWritten  int `json:"expensesWritten"`
	IncomeWritten    int `json:"incomeWritten"`
	LinkedExisting   int `json:"linkedExisting"`
	IgnoredTransfers int `json:"ignoredTransfers"`
	BalancesUpdated  int `json:"balancesUpdated"`
}

// Connection is a stored bank connection (one Pluggy Item).
type Connection struct {
	ID                uuid.UUID
	ItemID            string
	ConnectorName     *string
	InstitutionName   *string
	Status            *string
	ExecutionStatus   *string
	LastError         *string
	ProviderUpdatedAt *time.Time
	LastSyncedAt      *time.Time
	SyncRequestedAt   *time.Time
	AccountCount      int
}

// NeedsAttention reports whether the connection is stuck until the household re-authorises it in
// Meu Pluggy (expired login, MFA, revoked consent) — the cases worth a notification.
func NeedsAttention(status, executionStatus string) bool {
	switch status {
	case "LOGIN_ERROR", "WAITING_USER_INPUT", "WAITING_USER_ACTION", "OUTDATED":
		return true
	}
	switch executionStatus {
	case "INVALID_CREDENTIALS", "USER_INPUT_TIMEOUT", "SITE_NOT_AVAILABLE_FOR_LOGIN":
		return true
	}
	return false
}

// SyncUsable reports whether Pluggy's data for an item can be trusted right now. PARTIAL_SUCCESS
// is accepted (some products failed, the rest is fine); an item that is still updating or failed
// is skipped rather than mirrored half-empty.
func SyncUsable(status, executionStatus string) bool {
	switch executionStatus {
	case "SUCCESS", "PARTIAL_SUCCESS":
		return true
	}
	return executionStatus == "" && status == "UPDATED"
}

// ---- read models -----------------------------------------------------------------------------

// AccountView is a synced bank/card account as the API shows it.
type AccountView struct {
	ID              uuid.UUID
	Institution     *string
	Type            string
	Subtype         *string
	Name            *string
	MaskedNumber    *string
	Balance         *string
	Currency        string
	CreditLimit     *string
	AvailableCredit *string
	UpdatedAt       time.Time
}

// BillView is a card statement.
type BillView struct {
	ID               uuid.UUID
	AccountID        uuid.UUID
	Institution      *string
	AccountName      *string
	DueDate          time.Time
	Competence       time.Time
	TotalAmount      string
	MinimumPayment   *string
	FinanceCharges   string
	TransactionCount int
	// PaidAt is when the statement was paid in full: a card payment of exactly the total, on the
	// card itself ("Pagamento recebido") or from a bank account ("Pagamento de fatura"), between 30
	// days before and 5 days after the due date. A statement with nothing to pay counts as paid on
	// its due date. Nil: not (yet) seen paid — partial payments are not recognised.
	PaidAt *time.Time
}

// TransactionView is a raw bank transaction with what Atlas decided to do with it.
type TransactionView struct {
	ID               uuid.UUID
	AccountID        uuid.UUID
	AccountType      string
	Date             time.Time
	Amount           string
	Direction        string
	Status           string
	Description      string
	ProviderCategory *string
	Kind             ProjectionKind
	Overridden       bool
}

// TransactionFilter narrows ListTransactions.
type TransactionFilter struct {
	Kind        ProjectionKind
	OnlyIgnored bool
	Limit       int
	Offset      int
}

// InvestmentView is one synced investment position.
type InvestmentView struct {
	ID               uuid.UUID
	Institution      *string
	Name             string
	Code             *string
	Type             string
	Subtype          *string
	Status           *string
	Currency         string
	Balance          string
	AmountOriginal   *string
	AmountProfit     *string
	Quantity         *string
	AnnualRate       *string
	LastMonthRate    *string
	Last12MonthsRate *string
	DueDate          *time.Time
	LastSyncedAt     time.Time
}

// InvestmentPoint is one day of a position's balance history.
type InvestmentPoint struct {
	Date    time.Time
	Balance string
}
