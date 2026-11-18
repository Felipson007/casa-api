package pluggy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNew_RequiresCredentials(t *testing.T) {
	if _, err := New(Config{ClientID: "x"}); err == nil {
		t.Fatal("expected an error without a client secret")
	}
}

func TestDecimal(t *testing.T) {
	var v struct {
		A Decimal  `json:"a"`
		B Decimal  `json:"b"`
		C Decimal  `json:"c"`
		D *Decimal `json:"d"`
	}
	if err := json.Unmarshal([]byte(`{"a": 1234.5, "b": "-0.005", "c": null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.Money() != "1234.50" {
		t.Errorf("A = %s", v.A.Money())
	}
	if v.B.Money() != "-0.01" { // half away from zero
		t.Errorf("B = %s", v.B.Money())
	}
	if v.C.IsSet() || v.C.Money() != "0.00" {
		t.Errorf("null must be unset, got %q", v.C)
	}
	if v.A.Neg().Money() != "-1234.50" || v.A.Sign() != 1 {
		t.Errorf("neg/sign wrong")
	}
	if err := json.Unmarshal([]byte(`{"a":"abc"}`), &v); err == nil {
		t.Error("garbage must not decode")
	}
}

func TestDecimal_NoFloatDrift(t *testing.T) {
	var d Decimal
	if err := json.Unmarshal([]byte(`0.1`), &d); err != nil {
		t.Fatal(err)
	}
	sum := d.Rat()
	sum.Add(sum, d.Rat()).Add(sum, d.Rat()) // 0.1*3
	if sum.FloatString(2) != "0.30" {
		t.Errorf("0.1*3 = %s", sum.FloatString(2))
	}
}

func TestDateOf_DoesNotShiftTimezone(t *testing.T) {
	if got := DateOf("2026-08-31T23:30:00-03:00"); got != "2026-08-31" {
		t.Errorf("got %s", got)
	}
	if DateOf("2026") != "" {
		t.Error("short input must yield empty")
	}
}

func TestAuthIsCachedAndRenewedOn401(t *testing.T) {
	var auths, calls int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			n := atomic.AddInt32(&auths, 1)
			_ = json.NewEncoder(w).Encode(map[string]string{"apiKey": map[int32]string{1: "old", 2: "new"}[n]})
		case "/items/abc":
			atomic.AddInt32(&calls, 1)
			if r.Header.Get("X-API-KEY") != "new" && atomic.LoadInt32(&auths) < 2 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"id":"abc","status":"UPDATED","executionStatus":"SUCCESS"}`))
		}
	}))
	it, err := c.Item(context.Background(), "abc")
	if err != nil || it.Status != "UPDATED" {
		t.Fatalf("item = %+v err = %v", it, err)
	}
	if auths != 2 {
		t.Errorf("expected exactly one renewal (2 auths), got %d", auths)
	}
	// Second call reuses the cached key: no new /auth.
	if _, err := c.Item(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
	if auths != 2 {
		t.Errorf("key was not cached: %d auths", auths)
	}
}

func TestAPIError_And_RateLimit(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			_, _ = w.Write([]byte(`{"apiKey":"k"}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"BEFORE_ALLOWED_FREQUENCY","message":"too soon"}`))
	}))
	_, err := c.RefreshItem(context.Background(), "abc")
	if !IsRateLimited(err) {
		t.Fatalf("expected rate limit, got %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Error("error must never echo credentials")
	}
}

func TestAccounts_Paginates(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			_, _ = w.Write([]byte(`{"apiKey":"k"}`))
			return
		}
		if r.URL.Query().Get("itemId") != "it" {
			t.Errorf("itemId missing")
		}
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`{"total":2,"totalPages":2,"results":[{"id":"a1","type":"BANK","balance":10.5}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"total":2,"totalPages":2,"results":[{"id":"a2","type":"CREDIT","creditData":{"creditLimit":5000,"availableCreditLimit":1200.25}}]}`))
	}))
	accts, err := c.Accounts(context.Background(), "it")
	if err != nil || len(accts) != 2 {
		t.Fatalf("accts = %+v err = %v", accts, err)
	}
	if accts[0].Balance.Money() != "10.50" || accts[1].CreditData.AvailableCreditLimit.Money() != "1200.25" {
		t.Errorf("decimals wrong: %+v", accts)
	}
}

func TestTransactions_CursorAndGuards(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			_, _ = w.Write([]byte(`{"apiKey":"k"}`))
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"results":[
				{"id":"t1","accountId":"acc","date":"2026-08-10T12:00:00.000Z","amount":-50,"type":"DEBIT","status":"POSTED"},
				{"id":"t1","accountId":"acc","date":"2026-08-10T12:00:00.000Z","amount":-50,"type":"DEBIT","status":"POSTED"},
				{"id":"old","accountId":"acc","date":"2025-01-01T00:00:00.000Z","amount":-1,"type":"DEBIT","status":"POSTED"}
			],"next":"?accountId=acc&cursor=2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"t2","accountId":"acc","date":"2026-08-11","amount":99.9,"type":"CREDIT","status":"PENDING"}]}`))
	}))
	txs, err := c.Transactions(context.Background(), "acc", "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 2 || txs[0].ID != "t1" || txs[1].ID != "t2" {
		t.Fatalf("expected deduped, in-window rows [t1 t2], got %+v", txs)
	}
}

func TestTransactions_RejectsCursorForOtherAccount(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			_, _ = w.Write([]byte(`{"apiKey":"k"}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[],"next":"?accountId=other&cursor=2"}`))
	}))
	if _, err := c.Transactions(context.Background(), "acc", "2026-01-01", "2026-12-31"); err == nil {
		t.Fatal("cursor pointing at another account must be rejected")
	}
}

func TestBillsAndInvestments_404IsEmpty(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			_, _ = w.Write([]byte(`{"apiKey":"k"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	if b, err := c.Bills(context.Background(), "a"); err != nil || len(b) != 0 {
		t.Errorf("bills: %v %v", b, err)
	}
	if i, err := c.Investments(context.Background(), "a"); err != nil || len(i) != 0 {
		t.Errorf("investments: %v %v", i, err)
	}
}
