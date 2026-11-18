package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// googleCalendarScope grants read/write access to the household's calendars. Requesting only this
// scope (plus the consent flow forcing offline access) is enough to list calendars and CRUD events.
const googleCalendarScope = "https://www.googleapis.com/auth/calendar"

// GoogleCalendarCredentials is everything Atlas needs to read/write the household's Google
// Calendar. Stored AES-GCM-encrypted in external_integrations (type GOOGLE_CALENDAR), like the
// iCloud/Pluggy blobs; the refresh token never leaves the server.
type GoogleCalendarCredentials struct {
	RefreshToken string     `json:"refreshToken"`
	CalendarID   string     `json:"calendarId,omitempty"`
	CalendarName string     `json:"calendarName,omitempty"`
	LastSyncedAt *time.Time `json:"lastSyncedAt,omitempty"`
	LastError    string     `json:"lastError,omitempty"`
}

// ConnectGoogleCalendarRequest selects which Google calendar to sync (the refresh token comes from
// the OAuth callback, not from this body).
type ConnectGoogleCalendarRequest struct {
	CalendarID   string `json:"calendarId"`
	CalendarName string `json:"calendarName"`
}

// GoogleCalendarOAuthConfig is the OAuth2 client for the Calendar scope — same client id/secret as
// sign-in and Drive, different scope. Mirrors GoogleDriveOAuthConfig (offline + forced consent so
// Google always returns a refresh token).
type GoogleCalendarOAuthConfig struct {
	oauth2 *oauth2.Config
}

func NewGoogleCalendarOAuthConfig(clientID, clientSecret, redirectURL string) *GoogleCalendarOAuthConfig {
	return &GoogleCalendarOAuthConfig{oauth2: &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{googleCalendarScope},
		Endpoint:     google.Endpoint,
	}}
}

func (g *GoogleCalendarOAuthConfig) Enabled() bool {
	return g != nil && g.oauth2.ClientID != "" && g.oauth2.ClientID != "changeme"
}

func (g *GoogleCalendarOAuthConfig) AuthCodeURL(state string) string {
	return g.oauth2.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// Config exposes the oauth2 config so the googlecal service can build an authenticated client from
// the stored refresh token.
func (g *GoogleCalendarOAuthConfig) Config() *oauth2.Config { return g.oauth2 }

// ExchangeAndFetchAccount completes the code exchange and resolves the authorized account's email
// via the Calendar API's own calendarList (which works with the calendar scope alone).
func (g *GoogleCalendarOAuthConfig) ExchangeAndFetchAccount(ctx context.Context, code string) (refreshToken, email string, err error) {
	token, err := g.oauth2.Exchange(ctx, code)
	if err != nil {
		return "", "", fmt.Errorf("integrations: exchanging google calendar oauth code: %w", err)
	}
	if token.RefreshToken == "" {
		return "", "", fmt.Errorf("o Google não devolveu um refresh token — revogue o acesso em " +
			"myaccount.google.com/permissions e tente conectar de novo")
	}

	client := g.oauth2.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/calendar/v3/users/me/calendarList?maxResults=1")
	if err != nil {
		return "", "", fmt.Errorf("integrations: fetching google calendar account: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("integrations: google calendar calendarList returned %d", resp.StatusCode)
	}
	var list struct {
		Summary string `json:"summary"`
		Items   []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", "", fmt.Errorf("integrations: decoding google calendar account: %w", err)
	}
	// The calendarList doesn't carry the email; fall back to the primary calendar id (which is the
	// account's address) when it looks like one.
	email = list.Summary
	for _, it := range list.Items {
		if strings.Contains(it.ID, "@") {
			email = it.ID
			break
		}
	}
	if email == "" {
		email = "conta Google"
	}
	return token.RefreshToken, email, nil
}

// BeginGoogleCalendarConnect generates a fresh OAuth state and the consent-screen URL.
func (s *Service) BeginGoogleCalendarConnect() (state, authURL string, err error) {
	if !s.googleCalendar.Enabled() {
		return "", "", apperr.Internal(fmt.Errorf("integrations: google calendar oauth is not configured"))
	}
	state, err = generateOAuthState()
	if err != nil {
		return "", "", err
	}
	return state, s.googleCalendar.AuthCodeURL(state), nil
}

// CompleteGoogleCalendarConnect stores the refresh token after the OAuth callback (mirrors
// CompleteGoogleDriveConnect). The calendar selection is a separate step.
func (s *Service) CompleteGoogleCalendarConnect(ctx context.Context, code string) (ExternalIntegrationDTO, error) {
	if !s.googleCalendar.Enabled() {
		return ExternalIntegrationDTO{}, apperr.Internal(fmt.Errorf("integrations: google calendar oauth is not configured"))
	}
	refreshToken, email, err := s.googleCalendar.ExchangeAndFetchAccount(ctx, code)
	if err != nil {
		return ExternalIntegrationDTO{}, err
	}
	existing, err := s.repo.FindExternalByType(ctx, TypeGoogleCalendar)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: looking up google calendar connection: %w", err)
	}
	id := uuid.New()
	creds := GoogleCalendarCredentials{RefreshToken: refreshToken}
	if existing != nil {
		id = existing.ID
		if prev, derr := decodeGoogleCalendar(existing); derr == nil {
			creds.CalendarID, creds.CalendarName = prev.CalendarID, prev.CalendarName
			creds.LastSyncedAt, creds.LastError = prev.LastSyncedAt, prev.LastError
		}
	}
	if err := storeGoogleCalendar(ctx, s.repo, id, creds, &email); err != nil {
		return ExternalIntegrationDTO{}, err
	}
	persisted, err := s.repo.FindExternalByType(ctx, TypeGoogleCalendar)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: re-reading after google calendar connect: %w", err)
	}
	return toExternalDTO(*persisted), nil
}

// ConnectGoogleCalendar sets/changes the selected calendar on an already-authorized connection.
func (s *Service) ConnectGoogleCalendar(ctx context.Context, req ConnectGoogleCalendarRequest) (ExternalIntegrationDTO, error) {
	existing, err := s.repo.FindExternalByType(ctx, TypeGoogleCalendar)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: looking up google calendar connection: %w", err)
	}
	if existing == nil {
		return ExternalIntegrationDTO{}, apperr.Validation("authorize Google Calendar first")
	}
	creds, err := decodeGoogleCalendar(existing)
	if err != nil {
		return ExternalIntegrationDTO{}, err
	}
	if id := strings.TrimSpace(req.CalendarID); id != "" {
		creds.CalendarID = id
		creds.CalendarName = strings.TrimSpace(req.CalendarName)
	}
	email := ""
	if existing.ConnectedAccount != nil {
		email = *existing.ConnectedAccount
	}
	if err := storeGoogleCalendar(ctx, s.repo, existing.ID, creds, &email); err != nil {
		return ExternalIntegrationDTO{}, err
	}
	persisted, err := s.repo.FindExternalByType(ctx, TypeGoogleCalendar)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: re-reading after google calendar selection: %w", err)
	}
	return toExternalDTO(*persisted), nil
}

// GoogleCalendarCredentials returns the decrypted credentials + OAuth config, or ok=false.
func (s *Service) GoogleCalendarCredentials(ctx context.Context) (creds GoogleCalendarCredentials, ok bool, err error) {
	existing, err := s.repo.FindExternalByType(ctx, TypeGoogleCalendar)
	if err != nil {
		return GoogleCalendarCredentials{}, false, fmt.Errorf("integrations: looking up google calendar connection: %w", err)
	}
	if existing == nil {
		return GoogleCalendarCredentials{}, false, nil
	}
	creds, err = decodeGoogleCalendar(existing)
	if err != nil {
		return GoogleCalendarCredentials{}, false, err
	}
	return creds, true, nil
}

// GoogleCalendarOAuth returns the OAuth config (for building an authenticated client).
func (s *Service) GoogleCalendarOAuth() *GoogleCalendarOAuthConfig { return s.googleCalendar }

// RecordGoogleCalendarSync updates the sync bookkeeping (best-effort).
func (s *Service) RecordGoogleCalendarSync(ctx context.Context, lastSyncedAt time.Time, syncErr string) error {
	existing, err := s.repo.FindExternalByType(ctx, TypeGoogleCalendar)
	if err != nil || existing == nil {
		return err
	}
	creds, err := decodeGoogleCalendar(existing)
	if err != nil {
		return err
	}
	if syncErr == "" {
		creds.LastSyncedAt = &lastSyncedAt
		creds.LastError = ""
	} else {
		creds.LastError = syncErr
	}
	email := ""
	if existing.ConnectedAccount != nil {
		email = *existing.ConnectedAccount
	}
	return storeGoogleCalendar(ctx, s.repo, existing.ID, creds, &email)
}

func storeGoogleCalendar(ctx context.Context, repo Repository, id uuid.UUID, creds GoogleCalendarCredentials, account *string) error {
	payload, err := json.Marshal(creds)
	if err != nil {
		return apperr.Internal(err)
	}
	encrypted, err := Encrypt(string(payload))
	if err != nil {
		return apperr.Internal(err)
	}
	return repo.UpsertExternal(ctx, ExternalIntegration{
		ID:                    id,
		Type:                  TypeGoogleCalendar,
		ConnectedAccount:      account,
		RefreshTokenEncrypted: encrypted.Ciphertext,
		RefreshTokenIV:        encrypted.IV,
	})
}

func decodeGoogleCalendar(e *ExternalIntegration) (GoogleCalendarCredentials, error) {
	plain, err := Decrypt(e.RefreshTokenEncrypted, e.RefreshTokenIV)
	if err != nil {
		return GoogleCalendarCredentials{}, fmt.Errorf("integrations: decrypting google calendar credentials: %w", err)
	}
	var creds GoogleCalendarCredentials
	if err := json.Unmarshal([]byte(plain), &creds); err != nil {
		return GoogleCalendarCredentials{}, fmt.Errorf("integrations: stored google calendar credentials are malformed: %w", err)
	}
	return creds, nil
}
