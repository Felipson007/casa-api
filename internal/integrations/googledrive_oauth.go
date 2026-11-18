package integrations

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// googleDriveScope mirrors GoogleDriveIntegracaoService.SCOPE exactly — grants access only to
// files this app itself creates, not the household's whole Drive.
const googleDriveScope = "https://www.googleapis.com/auth/drive.file"

// GoogleDriveOAuthConfig is the OAuth2 client for connecting the household's Google Drive —
// distinct from auth.GoogleOAuthConfig (which is for signing in): same GOOGLE_CLIENT_ID/SECRET,
// different scope. Requests offline access and forces the consent screen every time
// (AccessTypeOffline + ApprovalForce) so Google always issues a refresh token: Google only issues
// one on a user's FIRST authorization of a given scope, silently omitting it on a repeat consent
// unless force-reprompted — mirrors GoogleDriveIntegracaoService.processarCallback's own comment on
// this exact failure mode.
type GoogleDriveOAuthConfig struct {
	oauth2 *oauth2.Config
}

// NewGoogleDriveOAuthConfig builds the client. redirectURL must exactly match one already
// authorized in Google Cloud Console for this client id — this rewrite uses an English path
// (/api/integrations/google-drive/callback, translated from Java's /api/integracoes/google-drive/
// callback per this codebase's naming convention), which needs adding as an authorized redirect
// URI in Google Cloud Console before this flow works.
func NewGoogleDriveOAuthConfig(clientID, clientSecret, redirectURL string) *GoogleDriveOAuthConfig {
	return &GoogleDriveOAuthConfig{oauth2: &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{googleDriveScope},
		Endpoint:     google.Endpoint,
	}}
}

// Enabled reports whether real credentials were provided — mirrors auth.GoogleOAuthConfig.Enabled.
func (g *GoogleDriveOAuthConfig) Enabled() bool {
	return g != nil && g.oauth2.ClientID != "" && g.oauth2.ClientID != "changeme"
}

// AuthCodeURL builds the redirect target for the connect endpoint.
func (g *GoogleDriveOAuthConfig) AuthCodeURL(state string) string {
	return g.oauth2.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// driveAboutUser is the subset of the Drive API's "about" response this needs.
type driveAboutUser struct {
	User struct {
		EmailAddress string `json:"emailAddress"`
	} `json:"user"`
}

// ExchangeAndFetchAccount completes the authorization-code exchange and resolves which Google
// account was authorized — mirrors GoogleDriveIntegracaoService.processarCallback's first half
// (token exchange + buscarEmailDaConta), stopping short of persisting (the caller does that).
//
// Resolves the account via the Drive API's own GET /drive/v3/about?fields=user endpoint, NOT
// Google's generic OAuth userinfo endpoint (oauth2/v3/userinfo) — that one requires an
// openid/email/profile-scoped token and returns 401 given only the drive.file scope this flow
// requests. Discovered live: the first real connect attempt against production failed with
// exactly that 401, since Java's own GoogleOAuthClient.buscarEmailDaConta already uses the Drive
// "about" endpoint for this same reason (it works with drive.file alone) — this was mistakenly
// substituted with the userinfo endpoint here instead of being read from source.
func (g *GoogleDriveOAuthConfig) ExchangeAndFetchAccount(ctx context.Context, code string) (refreshToken, email string, err error) {
	token, err := g.oauth2.Exchange(ctx, code)
	if err != nil {
		return "", "", fmt.Errorf("integrations: exchanging google drive oauth code: %w", err)
	}
	if token.RefreshToken == "" {
		return "", "", fmt.Errorf("o Google não devolveu um refresh token — revogue o acesso em " +
			"myaccount.google.com/permissions e tente conectar de novo")
	}

	client := g.oauth2.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/drive/v3/about?fields=user")
	if err != nil {
		return "", "", fmt.Errorf("integrations: fetching google drive account info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("integrations: google drive about endpoint returned %d", resp.StatusCode)
	}
	var about driveAboutUser
	if err := json.NewDecoder(resp.Body).Decode(&about); err != nil {
		return "", "", fmt.Errorf("integrations: decoding google drive account info: %w", err)
	}
	if about.User.EmailAddress == "" {
		return "", "", fmt.Errorf("integrations: google drive about endpoint returned no account email")
	}
	return token.RefreshToken, about.User.EmailAddress, nil
}

// generateOAuthState returns a random, URL-safe, single-use CSRF token — mirrors
// GoogleOAuthClient.gerarState. Verified via a short-lived cookie (see handler.go) instead of a
// server-side HTTP session, since this Go app has no generic per-request session store (its
// `sessions` table is specifically the login mechanism, not general-purpose state).
func generateOAuthState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("integrations: generating oauth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
