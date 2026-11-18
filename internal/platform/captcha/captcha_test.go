package captcha

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTurnstileSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.FormValue("secret"); got != "s3cret" {
			t.Errorf("secret = %q", got)
		}
		if got := r.FormValue("response"); got != "token-123" {
			t.Errorf("response = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	v := NewTurnstile("s3cret")
	v.url = server.URL
	v.client = server.Client()

	if err := v.Verify(context.Background(), "token-123", "203.0.113.7"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestTurnstileRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
	}))
	defer server.Close()

	v := NewTurnstile("s3cret")
	v.url = server.URL
	v.client = server.Client()

	if err := v.Verify(context.Background(), "bad", ""); err == nil {
		t.Fatal("expected verification failure")
	}
}

func TestTurnstileEmptyTokenFailsFast(t *testing.T) {
	v := NewTurnstile("s3cret")
	if err := v.Verify(context.Background(), "  ", ""); err == nil {
		t.Fatal("expected empty token to be rejected without a network call")
	}
}

func TestNewTurnstileUnconfigured(t *testing.T) {
	if NewTurnstile("") != nil {
		t.Fatal("empty secret must yield nil verifier")
	}
}

func TestVerifyNilVerifierIsNoop(t *testing.T) {
	if err := Verify(context.Background(), nil, "", ""); err != nil {
		t.Fatalf("nil verifier must skip the check, got %v", err)
	}
}
