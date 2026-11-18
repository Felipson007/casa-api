// Package pluggy is a small read-mostly REST client for the Pluggy Open Finance API
// (https://docs.pluggy.ai). It only knows how to authenticate, list what an Item holds and ask
// for a refresh — persistence and business rules live in internal/openfinance.
//
// Behaviour worth knowing:
//   - Auth: POST /auth with the client id/secret returns a short-lived apiKey. It is cached and
//     reused (Pluggy rate-limits /auth, and a fresh key per call multiplies every list request),
//     and transparently renewed on a 401/403.
//   - Money: every amount stays a Decimal (exact text), never float64.
//   - Pagination: /investments and /bills use page/pageSize; /v2/transactions uses an opaque
//     cursor. Both are bounded so a misbehaving upstream can't loop forever.
package pluggy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL is Pluggy's production API.
	DefaultBaseURL = "https://api.pluggy.ai"

	tokenTTL     = 100 * time.Minute // Pluggy keys live 2h; renew well before that.
	pageSize     = 500
	maxPages     = 200
	maxBodyBytes = 16 << 20
)

// Config configures a Client. ClientID and ClientSecret are required.
type Config struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
}

// Client talks to the Pluggy API. Safe for concurrent use.
type Client struct {
	baseURL      string
	clientID     string
	clientSecret string
	http         *http.Client
	now          func() time.Time

	mu       sync.Mutex
	apiKey   string
	tokenExp time.Time
}

// New builds a Client, or returns an error when credentials are missing.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, errors.New("pluggy: client id and secret are required")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: base, clientID: cfg.ClientID, clientSecret: cfg.ClientSecret, http: hc, now: time.Now}, nil
}

// APIError is a non-2xx answer from Pluggy. Message is Pluggy's own text; it never contains the
// credentials this client sent.
type APIError struct {
	Status  int
	Code    string
	Message string
	Path    string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("pluggy: %s -> HTTP %d", e.Path, e.Status)
	if e.Code != "" {
		msg += " (" + e.Code + ")"
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// IsRateLimited reports whether err is Pluggy asking us to slow down / not refresh yet.
func IsRateLimited(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Status == http.StatusTooManyRequests || ae.Code == "BEFORE_ALLOWED_FREQUENCY" || ae.Code == "RATE_LIMIT_EXCEEDED"
}

// IsNotFound reports whether err is a 404.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

func (c *Client) token(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.apiKey != "" && c.now().Before(c.tokenExp) {
		return c.apiKey, nil
	}
	body, _ := json.Marshal(map[string]string{"clientId": c.clientID, "clientSecret": c.clientSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/auth", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	var out struct {
		APIKey string `json:"apiKey"`
	}
	if err := c.roundTrip(req, "/auth", &out); err != nil {
		return "", err
	}
	if out.APIKey == "" {
		return "", errors.New("pluggy: /auth returned no apiKey")
	}
	c.apiKey = out.APIKey
	c.tokenExp = c.now().Add(tokenTTL)
	return c.apiKey, nil
}

func (c *Client) roundTrip(req *http.Request, path string, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error embeds the URL, which never carries secrets here (they travel in headers/body).
		return fmt.Errorf("pluggy: %s: %w", path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("pluggy: reading %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{Status: resp.StatusCode, Path: path}
		var payload struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &payload) == nil {
			ae.Code, ae.Message = payload.Code, payload.Message
		}
		return ae
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("pluggy: decoding %s: %w", path, err)
	}
	return nil
}

// do performs an authenticated request, renewing the apiKey once on 401/403.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	full := path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	for attempt := 0; ; attempt++ {
		key, err := c.token(ctx, attempt > 0)
		if err != nil {
			return err
		}
		var rdr io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return err
			}
			rdr = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+full, rdr)
		if err != nil {
			return err
		}
		req.Header.Set("X-API-KEY", key)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		err = c.roundTrip(req, path, out)
		var ae *APIError
		if attempt == 0 && errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
			continue // stale key: renew once and retry
		}
		return err
	}
}

// Item fetches one connection's current state.
func (c *Client) Item(ctx context.Context, itemID string) (Item, error) {
	var it Item
	err := c.do(ctx, http.MethodGet, "/items/"+url.PathEscape(itemID), nil, nil, &it)
	return it, err
}

// RefreshItem asks Pluggy to pull fresh data from the bank. It returns as soon as the request is
// accepted; the update itself takes minutes, so callers sync whatever is available now and the
// next scheduled sync picks up the refreshed data. IsRateLimited(err) means "too soon, ignore".
func (c *Client) RefreshItem(ctx context.Context, itemID string) (Item, error) {
	var it Item
	err := c.do(ctx, http.MethodPatch, "/items/"+url.PathEscape(itemID), nil, map[string]any{}, &it)
	return it, err
}

// Accounts lists every account under an Item.
func (c *Client) Accounts(ctx context.Context, itemID string) ([]Account, error) {
	return listPaged[Account](ctx, c, "/accounts", url.Values{"itemId": {itemID}})
}

// Bills lists the credit-card statements of one CREDIT account. Not every institution/connection
// type exposes them (Pluggy: mandatory on regulated Open Finance, limited on direct connections);
// an unsupported account yields an empty list or a 404, which is not an error for the caller.
func (c *Client) Bills(ctx context.Context, accountID string) ([]Bill, error) {
	bills, err := listPaged[Bill](ctx, c, "/bills", url.Values{"accountId": {accountID}})
	if IsNotFound(err) {
		return nil, nil
	}
	return bills, err
}

// Investments lists the positions under an Item. Items without an investments product answer with
// an empty list or a 404, both treated as "none".
func (c *Client) Investments(ctx context.Context, itemID string) ([]Investment, error) {
	inv, err := listPaged[Investment](ctx, c, "/investments", url.Values{"itemId": {itemID}})
	if IsNotFound(err) {
		return nil, nil
	}
	return inv, err
}

// Transactions lists an account's movements between from and to (YYYY-MM-DD, inclusive) using the
// v2 cursor pagination. Every returned row is verified to belong to the account and to fall inside
// the window; duplicates across pages are dropped.
func (c *Client) Transactions(ctx context.Context, accountID, from, to string) ([]Transaction, error) {
	q := url.Values{"accountId": {accountID}, "dateFrom": {from}, "dateTo": {to}}
	path := "/v2/transactions"
	query := q
	var out []Transaction
	seenID := map[string]bool{}
	for pages := 0; ; pages++ {
		if pages >= maxPages {
			return nil, fmt.Errorf("pluggy: transactions for one account exceeded %d pages; narrow the window", maxPages)
		}
		var resp struct {
			Results []Transaction `json:"results"`
			Next    string        `json:"next"`
		}
		if err := c.do(ctx, http.MethodGet, path, query, nil, &resp); err != nil {
			return nil, err
		}
		for _, t := range resp.Results {
			if t.AccountID != "" && t.AccountID != accountID {
				return nil, errors.New("pluggy: transaction returned for a different account")
			}
			d := DateOf(t.Date)
			if d == "" || d < from || d > to || seenID[t.ID] {
				continue
			}
			seenID[t.ID] = true
			out = append(out, t)
		}
		if resp.Next == "" {
			return out, nil
		}
		if !strings.HasPrefix(resp.Next, "?") {
			return nil, errors.New("pluggy: unexpected pagination cursor")
		}
		next, err := url.ParseQuery(strings.TrimPrefix(resp.Next, "?"))
		if err != nil {
			return nil, fmt.Errorf("pluggy: bad pagination cursor: %w", err)
		}
		if next.Get("accountId") != accountID {
			return nil, errors.New("pluggy: pagination cursor changed account")
		}
		query = next
	}
}

func listPaged[T any](ctx context.Context, c *Client, path string, base url.Values) ([]T, error) {
	var out []T
	for p := 1; p <= maxPages; p++ {
		q := url.Values{}
		for k, v := range base {
			q[k] = v
		}
		q.Set("page", strconv.Itoa(p))
		q.Set("pageSize", strconv.Itoa(pageSize))
		var pg page
		if err := c.do(ctx, http.MethodGet, path, q, nil, &pg); err != nil {
			return nil, err
		}
		for _, raw := range pg.Results {
			var v T
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, fmt.Errorf("pluggy: decoding %s item: %w", path, err)
			}
			out = append(out, v)
		}
		if pg.TotalPages <= p || len(pg.Results) == 0 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("pluggy: %s exceeded %d pages", path, maxPages)
}
