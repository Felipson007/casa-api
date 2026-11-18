package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GoogleOAuthConfig holds the OAuth2 client used for "Sign in with Google" — reuses the exact same
// registered client/redirect URI the old Java (Spring Security oauth2-client) setup already used,
// so no new Google Cloud Console configuration is needed (see GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET
// in .env; the local-testing pair is explicitly marked there as distinct from the production one).
type GoogleOAuthConfig struct {
	oauth2 *oauth2.Config
}

// NewGoogleOAuthConfig builds the client. redirectURL must exactly match one already authorized in
// Google Cloud Console for this client id — Spring Security's default
// "{baseUrl}/login/oauth2/code/{registrationId}" convention, which is why this package's HTTP
// routes are deliberately registered at the same /login/oauth2/code/google path (see routes.go):
// changing it would require registering a new redirect URI with Google first.
func NewGoogleOAuthConfig(clientID, clientSecret, redirectURL string) *GoogleOAuthConfig {
	return &GoogleOAuthConfig{oauth2: &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     google.Endpoint,
	}}
}

// Enabled reports whether real credentials were provided — false for the "changeme" placeholders,
// so main.go can skip registering the Google routes entirely rather than exposing an endpoint that
// would just fail Google's client-id check on every attempt.
func (g *GoogleOAuthConfig) Enabled() bool {
	return g != nil && g.oauth2.ClientID != "" && g.oauth2.ClientID != "changeme"
}

// AuthCodeURL builds the redirect target for GET /oauth2/authorization/google. prompt=select_account
// forces Google's account chooser every time instead of silently continuing with whatever Google
// account the browser already has active — without it, someone who switched which Google account
// they use gets silently re-authenticated with the OLD one (matching no app_users row) and never
// gets a chance to pick the right one, even in a fresh/incognito window.
func (g *GoogleOAuthConfig) AuthCodeURL(state string) string {
	return g.oauth2.AuthCodeURL(state, oauth2.SetAuthURLParam("prompt", "select_account"))
}

// googleUserInfo is the subset of Google's OIDC userinfo response this app needs.
type googleUserInfo struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

// ExchangeAndFetchEmail completes the authorization-code exchange and fetches the verified email
// from Google's userinfo endpoint — the only claim this app trusts Google for (no signup: the
// email must already match an app_users row, checked by the caller via LoginWithGoogleEmail).
func (g *GoogleOAuthConfig) ExchangeAndFetchEmail(ctx context.Context, code string) (string, error) {
	token, err := g.oauth2.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("auth: exchanging google oauth code: %w", err)
	}
	client := g.oauth2.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
	if err != nil {
		return "", fmt.Errorf("auth: fetching google userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth: google userinfo returned %d", resp.StatusCode)
	}
	var info googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("auth: decoding google userinfo: %w", err)
	}
	if !info.EmailVerified || info.Email == "" {
		return "", fmt.Errorf("auth: google account has no verified email")
	}
	return info.Email, nil
}
