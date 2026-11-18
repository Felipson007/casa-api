// Package captcha verifies a client-supplied captcha token server-side. The only implementation is
// Cloudflare Turnstile; the Verifier interface keeps the auth handlers testable without a live
// Cloudflare call.
package captcha

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// defaultVerifyURL is Cloudflare Turnstile's siteverify endpoint.
const defaultVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// Verifier validates a captcha token. A nil Verifier means "captcha not configured": callers must
// skip the check entirely rather than failing every request.
type Verifier interface {
	Verify(ctx context.Context, token, remoteIP string) error
}

// Turnstile verifies Cloudflare Turnstile tokens against siteverify.
type Turnstile struct {
	secret string
	client *http.Client
	url    string
}

// NewTurnstile builds a verifier from the secret key. An empty secret returns nil (not configured).
func NewTurnstile(secret string) *Turnstile {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil
	}
	return &Turnstile{
		secret: secret,
		client: &http.Client{Timeout: 5 * time.Second},
		url:    defaultVerifyURL,
	}
}

// Verify posts the token to siteverify. A missing/empty token is rejected without a network call,
// so a client that forgot to attach one fails fast. Every failure is a 400-class validation error
// to the caller — the response never echoes Cloudflare's error codes.
func (t *Turnstile) Verify(ctx context.Context, token, remoteIP string) error {
	if strings.TrimSpace(token) == "" {
		return apperr.Validation("captcha verification is required")
	}
	form := url.Values{}
	form.Set("secret", t.secret)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, strings.NewReader(form.Encode()))
	if err != nil {
		return apperr.Internal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return apperr.Internal(fmt.Errorf("captcha: verifying token: %w", err))
	}
	defer resp.Body.Close()

	var body struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return apperr.Internal(fmt.Errorf("captcha: decoding siteverify response: %w", err))
	}
	if !body.Success {
		return apperr.Validation("captcha verification failed")
	}
	return nil
}

// Verify runs a configured verifier, or is a no-op when v is nil. Convenience for handlers that
// hold a possibly-nil Verifier.
func Verify(ctx context.Context, v Verifier, token, remoteIP string) error {
	if v == nil {
		return nil
	}
	return v.Verify(ctx, token, remoteIP)
}
