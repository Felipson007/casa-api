package openfinance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/integrations"
	"github.com/atlas/atlas-go/internal/notifications"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/atlas/atlas-go/internal/platform/money"
	"github.com/atlas/atlas-go/internal/pluggy"
)

const (
	// DefaultInterval is how often the worker re-syncs a connection on its own. Pluggy itself
	// refreshes items from the banks on its own schedule, so polling faster than a few hours only
	// re-reads the same data.
	DefaultInterval = 6 * time.Hour
	// backfillDays is the first-sync history window (Pluggy exposes at most 12 months).
	backfillDays = 365
	// overlapDays re-reads the tail of the previous window: PENDING rows turn POSTED and late
	// rows appear a few days after the fact.
	overlapDays = 14
	// cardWindowDays is the minimum window for card accounts: a card charge can reach the bank
	// feed weeks after the purchase (often only when the statement closes), dated on the purchase
	// day — older than overlapDays, so an incremental sync never asked for it again and it was
	// lost. Two months covers the open statement and the previous one.
	cardWindowDays = 60
	// autoRetryGap stops a failing connection from being hammered every worker tick.
	autoRetryGap = 15 * time.Minute
)

// API is the slice of the Pluggy client the sync needs (an interface so tests can fake it).
type API interface {
	Item(ctx context.Context, itemID string) (pluggy.Item, error)
	RefreshItem(ctx context.Context, itemID string) (pluggy.Item, error)
	Accounts(ctx context.Context, itemID string) ([]pluggy.Account, error)
	Transactions(ctx context.Context, accountID, from, to string) ([]pluggy.Transaction, error)
	Bills(ctx context.Context, accountID string) ([]pluggy.Bill, error)
	Investments(ctx context.Context, itemID string) ([]pluggy.Investment, error)
}

// APIFactory builds an API from credentials.
type APIFactory func(cfg pluggy.Config) (API, error)

// Repository is defined here, at the point of consumption.
type Repository interface {
	RecordItem(ctx context.Context, itemID string, item pluggy.Item, syncErr string) (prevStatus string, err error)
	EnsureConnections(ctx context.Context, itemIDs []string) error
	LastSyncedAt(ctx context.Context, itemID string) (*time.Time, error)
	Save(ctx context.Context, snap Snapshot) (SaveResult, error)
	RequestSync(ctx context.Context) (bool, error)
	TakeSyncRequest(ctx context.Context) (bool, error)
	ListConnections(ctx context.Context) ([]Connection, error)
	ListAccounts(ctx context.Context) ([]AccountView, error)
	ListBills(ctx context.Context, limit int) ([]BillView, error)
	ListTransactions(ctx context.Context, f TransactionFilter) ([]TransactionView, error)
	SetProjection(ctx context.Context, id uuid.UUID, kind ProjectionKind) error
	ListInvestments(ctx context.Context) ([]InvestmentView, error)
	InvestmentHistory(ctx context.Context, id uuid.UUID) ([]InvestmentPoint, error)
}

// CredentialSource yields the Pluggy credentials (ok=false: not connected).
type CredentialSource interface {
	PluggyCredentials(ctx context.Context) (integrations.PluggyCredentials, bool, error)
}

// Connector stores the Pluggy connection (implemented by integrations.Service).
type Connector interface {
	ConnectPluggy(ctx context.Context, req integrations.ConnectPluggyRequest) (integrations.ExternalIntegrationDTO, error)
}

// Notifier records in-app notifications.
type Notifier interface {
	Create(ctx context.Context, req notifications.CreateNotificationRequest) (notifications.NotificationDTO, error)
}

// Deps wires a Service. Only Repo, Credentials and NewAPI are mandatory.
type Deps struct {
	Repo        Repository
	Credentials CredentialSource
	Connector   Connector
	Notifier    Notifier
	NewAPI      APIFactory
	Now         func() time.Time
	Logger      *slog.Logger
	Interval    time.Duration
	BaseURL     string
}

// Service orchestrates syncing and exposes the read side.
type Service struct {
	Deps
	mu          sync.Mutex
	lastAttempt time.Time
}

// NewService builds a Service, filling defaults.
func NewService(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Interval <= 0 {
		d.Interval = DefaultInterval
	}
	if d.NewAPI == nil {
		d.NewAPI = func(cfg pluggy.Config) (API, error) { return pluggy.New(cfg) }
	}
	return &Service{Deps: d}
}

// EnvCredentials is a CredentialSource reading PLUGGY_CLIENT_ID / PLUGGY_CLIENT_SECRET /
// PLUGGY_ITEM_IDS — a headless fallback for when nobody has connected Pluggy in the UI.
type EnvCredentials struct{}

func (EnvCredentials) PluggyCredentials(context.Context) (integrations.PluggyCredentials, bool, error) {
	id, secret := strings.TrimSpace(os.Getenv("PLUGGY_CLIENT_ID")), strings.TrimSpace(os.Getenv("PLUGGY_CLIENT_SECRET"))
	if id == "" || secret == "" {
		return integrations.PluggyCredentials{}, false, nil
	}
	var ids []string
	for _, p := range strings.FieldsFunc(os.Getenv("PLUGGY_ITEM_IDS"), func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
		if _, err := uuid.Parse(p); err == nil {
			ids = append(ids, p)
		}
	}
	return integrations.PluggyCredentials{ClientID: id, ClientSecret: secret, ItemIDs: ids}, len(ids) > 0, nil
}

// ChainCredentials tries each source in order and returns the first that is connected.
type ChainCredentials []CredentialSource

func (c ChainCredentials) PluggyCredentials(ctx context.Context) (integrations.PluggyCredentials, bool, error) {
	var firstErr error
	for _, s := range c {
		creds, ok, err := s.PluggyCredentials(ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if ok {
			return creds, true, nil
		}
	}
	return integrations.PluggyCredentials{}, false, firstErr
}

// ---- connect / status ----------------------------------------------------------------------

// Connect stores the credentials (encrypted) and flags an immediate first sync.
func (s *Service) Connect(ctx context.Context, req integrations.ConnectPluggyRequest) (integrations.ExternalIntegrationDTO, error) {
	if s.Connector == nil {
		return integrations.ExternalIntegrationDTO{}, apperr.Internal(errors.New("openfinance: no connector configured"))
	}
	dto, err := s.Connector.ConnectPluggy(ctx, req)
	if err != nil {
		return integrations.ExternalIntegrationDTO{}, err
	}
	creds, ok, err := s.Credentials.PluggyCredentials(ctx)
	if err != nil {
		return integrations.ExternalIntegrationDTO{}, err
	}
	if ok {
		if err := s.Repo.EnsureConnections(ctx, creds.ItemIDs); err != nil {
			return integrations.ExternalIntegrationDTO{}, fmt.Errorf("openfinance: registering connections: %w", err)
		}
	}
	return dto, nil
}

// StatusReport is the state shown under Configurações → Conexões.
type StatusReport struct {
	Configured  bool
	SyncPending bool
	Connections []Connection
}

// Status reports whether Pluggy is connected and how each connection is doing.
func (s *Service) Status(ctx context.Context) (StatusReport, error) {
	_, ok, err := s.Credentials.PluggyCredentials(ctx)
	if err != nil {
		return StatusReport{}, err
	}
	conns, err := s.Repo.ListConnections(ctx)
	if err != nil {
		return StatusReport{}, err
	}
	rep := StatusReport{Configured: ok, Connections: conns}
	for _, c := range conns {
		if c.SyncRequestedAt != nil {
			rep.SyncPending = true
		}
	}
	return rep, nil
}

// RequestSync asks the worker to sync now (and to ask Pluggy to refresh from the banks).
func (s *Service) RequestSync(ctx context.Context) error {
	_, ok, err := s.Credentials.PluggyCredentials(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.Validation("connect Pluggy first")
	}
	has, err := s.Repo.RequestSync(ctx)
	if err != nil {
		return err
	}
	if !has {
		// Connected but no connection row yet (e.g. credentials came from the environment).
		creds, _, _ := s.Credentials.PluggyCredentials(ctx)
		if err := s.Repo.EnsureConnections(ctx, creds.ItemIDs); err != nil {
			return err
		}
		_, err = s.Repo.RequestSync(ctx)
		return err
	}
	return nil
}

// ---- sync ----------------------------------------------------------------------------------

// ItemReport is the outcome of syncing one connection.
type ItemReport struct {
	Index   int // position in the configured list; item ids stay out of logs
	Skipped string
	Error   string
	Result  SaveResult
}

// Tick is what the worker calls every minute: it syncs when a manual request is pending, or when a
// connection has never synced / is older than the interval, never more often than autoRetryGap
// for automatic runs. Returns nil report when there was nothing to do.
func (s *Service) Tick(ctx context.Context) ([]ItemReport, error) {
	creds, ok, err := s.Credentials.PluggyCredentials(ctx)
	if err != nil || !ok {
		return nil, err
	}
	requested, err := s.Repo.TakeSyncRequest(ctx)
	if err != nil {
		return nil, err
	}
	if !requested {
		conns, err := s.Repo.ListConnections(ctx)
		if err != nil {
			return nil, err
		}
		now := s.Now()
		stale := len(conns) == 0
		for _, c := range conns {
			if c.LastSyncedAt == nil || now.Sub(*c.LastSyncedAt) >= s.Interval {
				stale = true
			}
		}
		s.mu.Lock()
		tooSoon := !s.lastAttempt.IsZero() && now.Sub(s.lastAttempt) < autoRetryGap
		s.mu.Unlock()
		if !stale || tooSoon {
			return nil, nil
		}
	}
	s.mu.Lock()
	s.lastAttempt = s.Now()
	s.mu.Unlock()
	return s.SyncAll(ctx, creds, requested)
}

// SyncAll syncs every configured item. One item failing never stops the others; the returned
// error is non-nil only when nothing could be attempted at all.
func (s *Service) SyncAll(ctx context.Context, creds integrations.PluggyCredentials, refresh bool) ([]ItemReport, error) {
	api, err := s.NewAPI(pluggy.Config{BaseURL: s.BaseURL, ClientID: creds.ClientID, ClientSecret: creds.ClientSecret})
	if err != nil {
		return nil, fmt.Errorf("openfinance: building pluggy client: %w", err)
	}
	reports := make([]ItemReport, 0, len(creds.ItemIDs))
	for i, itemID := range creds.ItemIDs {
		rep := ItemReport{Index: i}
		res, skipped, err := s.syncItem(ctx, api, itemID, refresh)
		rep.Result, rep.Skipped = res, skipped
		if err != nil {
			rep.Error = err.Error()
			s.Logger.Error("openfinance: item sync failed", "item", i, "cause", err.Error())
		} else if skipped != "" {
			s.Logger.Warn("openfinance: item skipped", "item", i, "reason", skipped)
		} else {
			s.Logger.Info("openfinance: item synced", "item", i, "accounts", res.Accounts, "transactions", res.Transactions,
				"expenses", res.ExpensesWritten, "income", res.IncomeWritten, "linked", res.LinkedExisting,
				"investments", res.Investments, "balances", res.BalancesUpdated)
		}
		reports = append(reports, rep)
	}
	return reports, nil
}

func (s *Service) syncItem(ctx context.Context, api API, itemID string, refresh bool) (SaveResult, string, error) {
	item, err := api.Item(ctx, itemID)
	if err != nil {
		_, _ = s.Repo.RecordItem(ctx, itemID, pluggy.Item{}, err.Error())
		return SaveResult{}, "", err
	}
	if refresh {
		// Best effort: the bank update takes minutes, so this run mirrors what Pluggy already has
		// and a later scheduled run picks up the refreshed data. "Too soon" is expected, not an error.
		if _, rerr := api.RefreshItem(ctx, itemID); rerr != nil && !pluggy.IsRateLimited(rerr) {
			s.Logger.Warn("openfinance: refresh request failed", "cause", rerr.Error())
		}
	}
	prev, err := s.Repo.RecordItem(ctx, itemID, item, "")
	if err != nil {
		return SaveResult{}, "", err
	}
	if NeedsAttention(item.Status, item.ExecutionStatus) && !NeedsAttention(prev, "") {
		s.notifyAttention(ctx, item)
	}
	if !SyncUsable(item.Status, item.ExecutionStatus) {
		return SaveResult{}, "item not ready: " + item.Status + "/" + item.ExecutionStatus, nil
	}

	snap, err := s.collect(ctx, api, itemID, item)
	if err != nil {
		_, _ = s.Repo.RecordItem(ctx, itemID, item, err.Error())
		return SaveResult{}, "", err
	}
	res, err := s.Repo.Save(ctx, snap)
	if err != nil {
		return SaveResult{}, "", fmt.Errorf("saving snapshot: %w", err)
	}
	return res, "", nil
}

// collect gathers everything for one item in memory. Any account failing aborts the whole item
// (a partial mirror would let the projection undercount); investments are separate — an item
// with no investments product is normal, and a failure there must not block spending data.
func (s *Service) collect(ctx context.Context, api API, itemID string, item pluggy.Item) (Snapshot, error) {
	now := s.Now()
	today := civildate.Of(now.In(civildate.Location())).Time()
	from := today.AddDate(0, 0, -backfillDays)
	if last, err := s.Repo.LastSyncedAt(ctx, itemID); err == nil && last != nil {
		if w := last.In(civildate.Location()).AddDate(0, 0, -overlapDays); w.After(from) {
			from = time.Date(w.Year(), w.Month(), w.Day(), 0, 0, 0, 0, time.UTC)
		}
	}
	fromS, toS := from.Format("2006-01-02"), today.Format("2006-01-02")

	accounts, err := api.Accounts(ctx, itemID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("listing accounts: %w", err)
	}
	snap := Snapshot{Item: item, ItemID: itemID, SyncedAt: now, WindowFrom: fromS}
	for _, a := range accounts {
		as := AccountSnapshot{Account: a}
		if a.Type != AccountTypeBank && a.Type != AccountTypeCredit {
			continue
		}
		accountFrom := fromS
		if a.Type == AccountTypeCredit {
			if cardFrom := today.AddDate(0, 0, -cardWindowDays).Format("2006-01-02"); cardFrom < accountFrom {
				accountFrom = cardFrom
			}
			as.WindowFrom = accountFrom
		}
		if as.Transactions, err = api.Transactions(ctx, a.ID, accountFrom, toS); err != nil {
			return Snapshot{}, fmt.Errorf("listing transactions: %w", err)
		}
		if a.Type == AccountTypeCredit {
			if as.Bills, err = api.Bills(ctx, a.ID); err != nil {
				return Snapshot{}, fmt.Errorf("listing bills: %w", err)
			}
		}
		snap.Accounts = append(snap.Accounts, as)
	}
	if inv, ierr := api.Investments(ctx, itemID); ierr != nil {
		s.Logger.Warn("openfinance: investments unavailable for item", "cause", ierr.Error())
	} else {
		snap.Investments, snap.InvestmentsFetched = inv, true
	}
	return snap, nil
}

func (s *Service) notifyAttention(ctx context.Context, item pluggy.Item) {
	if s.Notifier == nil {
		return
	}
	name := "uma conexão bancária"
	if item.Connector != nil && strings.TrimSpace(item.Connector.Name) != "" {
		name = item.Connector.Name
	}
	if _, err := s.Notifier.Create(ctx, notifications.CreateNotificationRequest{
		Type:    notifications.NotificationTypeOpenFinanceAction,
		Title:   "Reconecte " + name,
		Message: "A sincronização automática com " + name + " parou e precisa da sua autorização no Meu Pluggy. Enquanto isso, os dados não serão atualizados.",
	}); err != nil {
		s.Logger.Error("openfinance: recording notification failed", "cause", err.Error())
	}
}

// ---- read side and overrides ---------------------------------------------------------------

func (s *Service) Accounts(ctx context.Context) ([]AccountView, error) {
	return s.Repo.ListAccounts(ctx)
}

func (s *Service) Bills(ctx context.Context) ([]BillView, error) { return s.Repo.ListBills(ctx, 60) }

func (s *Service) Transactions(ctx context.Context, f TransactionFilter) ([]TransactionView, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	if f.Kind != "" && !f.Kind.Valid() {
		return nil, apperr.Validation("unknown kind", "kind")
	}
	return s.Repo.ListTransactions(ctx, f)
}

// SetProjection applies a human decision: count it (EXPENSE/INCOME), stop counting it
// (IGNORED_MANUAL), or hand it back to the classifier (AUTO → empty kind).
func (s *Service) SetProjection(ctx context.Context, id uuid.UUID, kind string) error {
	switch ProjectionKind(kind) {
	case KindExpense, KindIncome, KindIgnoredManual:
		return s.Repo.SetProjection(ctx, id, ProjectionKind(kind))
	}
	if kind == "AUTO" {
		return s.Repo.SetProjection(ctx, id, "")
	}
	return apperr.Validation("kind must be EXPENSE, INCOME, IGNORED_MANUAL or AUTO", "kind")
}

func (s *Service) Investments(ctx context.Context) ([]InvestmentView, error) {
	return s.Repo.ListInvestments(ctx)
}

// TotalInvestments sums the BRL balance of every open position — what dashboard.NetWorth adds to
// the household's net worth. Positions in another currency are left out rather than added at a
// guessed rate.
func (s *Service) TotalInvestments(ctx context.Context) (string, error) {
	views, err := s.Repo.ListInvestments(ctx)
	if err != nil {
		return "", err
	}
	total := money.Zero
	for _, v := range views {
		if v.Currency != "" && v.Currency != "BRL" {
			continue
		}
		if total, err = money.Add(total, v.Balance); err != nil {
			return "", err
		}
	}
	return total, nil
}

func (s *Service) InvestmentHistory(ctx context.Context, id uuid.UUID) ([]InvestmentPoint, error) {
	return s.Repo.InvestmentHistory(ctx, id)
}
