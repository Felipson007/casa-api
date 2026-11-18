package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/captcha"
	"github.com/atlas/atlas-go/internal/platform/eventlog"
	"github.com/atlas/atlas-go/internal/platform/httpx"
	"github.com/atlas/atlas-go/internal/platform/logging"
	"github.com/atlas/atlas-go/internal/platform/secretbox"
)

// oauthStateCookie carries the pending OAuth state across the redirect to Google and back —
// double-submit pattern (compare the callback's state param against this cookie), the same role
// Spring Security's own session-backed authorization request served in the old backend.
const oauthStateCookie = "atlas_oauth_state"

// mfaChallengeCookie carries the short-lived second-factor challenge when the login was started by
// a full-page redirect (Google OAuth), where a JSON body is not an option. HttpOnly: the frontend
// never needs to read it, it just posts the code.
const mfaChallengeCookie = "atlas_mfa_challenge"

// Handler serves the login/session HTTP surface. Registered on the PUBLIC mux (see routes.go) —
// none of these routes can require an existing session, since their entire purpose is creating one.
type Handler struct {
	store  Store
	google *GoogleOAuthConfig // nil if Google OAuth isn't configured (no real credentials)
	// captchaVerifier is nil when Turnstile isn't configured — the change-password handler then
	// skips the captcha check rather than rejecting every request.
	captchaVerifier captcha.Verifier
	// box encrypts each user's TOTP secret at rest (see internal/platform/secretbox). When nil,
	// MFA enrollment is refused rather than storing a decryptable secret.
	box *secretbox.Box
	// issuer is the label authenticator apps show for Atlas (otpauth issuer).
	issuer string
	// events records login/logout audit events. Never nil — NewHandler falls back to a discard
	// recorder so the handler is testable without the audit module.
	events eventlog.Recorder
}

func NewHandler(store Store, google *GoogleOAuthConfig, captchaVerifier captcha.Verifier, box *secretbox.Box, issuer string, events eventlog.Recorder) *Handler {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		issuer = "Atlas"
	}
	if events == nil {
		events = eventlog.NopRecorder{}
	}
	return &Handler{store: store, google: google, captchaVerifier: captchaVerifier, box: box, issuer: issuer, events: events}
}

// recordAuth emits one auth audit event with the request metadata the audit screen shows.
func (h *Handler) recordAuth(r *http.Request, t eventlog.Type, sev eventlog.Severity, userID *uuid.UUID, email, message string) {
	if h.events == nil {
		return
	}
	ev := eventlog.Event{
		Type:      t,
		Severity:  sev,
		UserID:    userID,
		UserEmail: email,
		IP:        httpx.ClientIP(r),
		UserAgent: r.UserAgent(),
		Method:    r.Method,
		Path:      r.URL.Path,
		RequestID: httpx.RequestIDFromContext(r.Context()),
		Message:   message,
		CreatedAt: time.Now(),
	}
	if u, ok := httpx.UserFromContext(r.Context()); ok && u.TenantID != uuid.Nil {
		tid := u.TenantID
		ev.TenantID = &tid
	}
	h.events.Record(r.Context(), ev)
}

// Login handles POST /api/auth/login (email + senha — see LoginRequest's doc comment on the field
// name). Sets the session cookie and returns the same profile shape GET /api/auth/me does, since
// the frontend's login mutation expects an AuthMeResponse back, not a bare token.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[LoginRequest](r)
	if err != nil {
		return err
	}
	userID, err := h.store.AuthenticatePassword(r.Context(), req.Email, req.Senha)
	if err != nil {
		h.recordAuth(r, eventlog.TypeLoginFailure, eventlog.SeverityWarning, nil, req.Email, "Falha de login: "+err.Error())
		return err
	}
	// Second factor: password is correct but MFA is on — hand back a short-lived challenge
	// instead of a session. LoginMFA (account_handler.go) completes it.
	encryptedSecret, mfaEnabled, err := h.store.MFASecret(r.Context(), userID)
	if err != nil {
		return err
	}
	if mfaEnabled && encryptedSecret != nil {
		raw, _, err := h.store.CreateMFAChallenge(r.Context(), userID)
		if err != nil {
			return err
		}
		h.recordAuth(r, eventlog.TypeLoginMFARequired, eventlog.SeverityInfo, &userID, req.Email, "Login aguardando segundo fator")
		httpx.WriteJSON(w, http.StatusOK, LoginResponse{MFARequired: true, MFAToken: raw})
		return nil
	}
	token, expiresAt, err := h.store.CreateSession(r.Context(), userID)
	if err != nil {
		return err
	}
	httpx.SetSessionCookie(w, r, token, expiresAt)
	h.recordAuth(r, eventlog.TypeLoginSuccess, eventlog.SeverityInfo, &userID, req.Email, "Login com senha")
	return h.writeLoginDone(w, r.Context(), token)
}

// Me handles GET /api/auth/me — the authenticated user's identity + permissions. Sits behind
// AuthMiddleware like every other module (registered on the authenticated mux, not here), so the
// user is already in context by the time this runs.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) error {
	u, ok := httpx.UserFromContext(r.Context())
	if !ok {
		return apperr.Unauthorized("no authenticated user in context")
	}
	return h.writeProfileForUser(w, r.Context(), u)
}

// Logout handles POST /api/auth/logout — deletes the session row (best-effort) and clears the
// cookie regardless of whether a row was found, so a stale/already-expired cookie still gets wiped
// client-side.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) error {
	var userID *uuid.UUID
	if c, err := r.Cookie(httpx.SessionCookieName); err == nil && c.Value != "" {
		// Resolve the user before deleting the session so the audit trail can pair the logout with
		// the login. Best-effort: a failed lookup still logs out.
		if u, err := h.store.LoadUserBySession(r.Context(), c.Value); err == nil && u.ID != uuid.Nil {
			id := u.ID
			userID = &id
		}
		_ = h.store.Logout(r.Context(), c.Value) // best-effort: a failed delete still clears the cookie below
	}
	h.recordAuth(r, eventlog.TypeLogout, eventlog.SeverityInfo, userID, "", "Logout")
	httpx.ClearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// BeginGoogleLogin handles GET /oauth2/authorization/google — the link the login page's "Continuar
// com Google" button navigates to (a real <a href>, not a fetch — see the frontend's login page).
func (h *Handler) BeginGoogleLogin(w http.ResponseWriter, r *http.Request) {
	logger := logging.FromContext(r.Context())
	if h.google == nil || !h.google.Enabled() {
		logger.Warn("google oauth login rejected", "reason", "google not configured")
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	state, err := randomState()
	if err != nil {
		logger.Error("google oauth login failed to generate state", "error", err)
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: state, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: 600, // 10 minutes — just long enough for the Google round trip
	})
	http.Redirect(w, r, h.google.AuthCodeURL(state), http.StatusFound)
}

// GoogleCallback handles GET /login/oauth2/code/google — Spring Security's default callback path
// for the "google" registration, kept identical on purpose so the redirect URI already registered
// in Google Cloud Console for this OAuth client keeps working unchanged (see GoogleOAuthConfig's
// doc comment). A full page redirect either way (success -> /dashboard, failure -> /login?error=1),
// matching the old backend's OAuth2LoginSuccessHandler/failure behavior the frontend already
// expects (see login/page.tsx's ErroDeLogin).
func (h *Handler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	logger := logging.FromContext(r.Context())
	fail := func(reason string, args ...any) {
		logger.Warn("google oauth callback rejected", append([]any{"reason", reason}, args...)...)
		h.recordAuth(r, eventlog.TypeLoginFailure, eventlog.SeverityWarning, nil, "", "Login Google recusado: "+reason)
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
	}

	if h.google == nil || !h.google.Enabled() {
		fail("google not configured")
		return
	}
	stateCookie, err := r.Cookie(oauthStateCookie)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		fail("state mismatch", "cookie_present", err == nil)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: "", Path: "/", MaxAge: -1})

	code := r.URL.Query().Get("code")
	if code == "" {
		fail("missing code", "google_error", r.URL.Query().Get("error"))
		return
	}
	email, err := h.google.ExchangeAndFetchEmail(r.Context(), code)
	if err != nil {
		fail("exchange failed", "cause", err.Error())
		return
	}
	userID, err := h.store.AuthenticateGoogleEmail(r.Context(), email)
	if err != nil {
		fail("no matching app_user for google email", "email", email, "cause", err.Error())
		return
	}
	// Google proves the email, not the second factor: an MFA-enabled account still has to complete
	// the challenge. The challenge token rides in an HttpOnly cookie and the user lands on the
	// login page's verification step.
	encryptedSecret, mfaEnabled, err := h.store.MFASecret(r.Context(), userID)
	if err != nil {
		fail("loading MFA state", "email", email, "cause", err.Error())
		return
	}
	if mfaEnabled && encryptedSecret != nil {
		raw, _, err := h.store.CreateMFAChallenge(r.Context(), userID)
		if err != nil {
			fail("creating MFA challenge", "email", email, "cause", err.Error())
			return
		}
		setMFAChallengeCookie(w, r, raw)
		h.recordAuth(r, eventlog.TypeLoginMFARequired, eventlog.SeverityInfo, &userID, email, "Login Google aguardando segundo fator")
		http.Redirect(w, r, "/login?mfa=1", http.StatusFound)
		return
	}
	token, expiresAt, err := h.store.CreateSession(r.Context(), userID)
	if err != nil {
		fail("creating session", "email", email, "cause", err.Error())
		return
	}
	httpx.SetSessionCookie(w, r, token, expiresAt)
	h.recordAuth(r, eventlog.TypeLoginSuccess, eventlog.SeverityInfo, &userID, email, "Login com Google")
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// writeLoginDone responds to a completed login (password only, or password + second factor) with
// the profile wrapped in a LoginResponse, so the frontend has the same shape whether or not MFA was
// involved.
func (h *Handler) writeLoginDone(w http.ResponseWriter, ctx context.Context, sessionToken string) error {
	u, err := h.store.LoadUserBySession(ctx, sessionToken)
	if err != nil {
		return apperr.Internal(err)
	}
	resp, err := h.buildMeResponse(ctx, u)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, LoginResponse{Me: &resp})
	return nil
}

func (h *Handler) writeProfileForUser(w http.ResponseWriter, ctx context.Context, u httpx.User) error {
	resp, err := h.buildMeResponse(ctx, u)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
	return nil
}

func (h *Handler) buildMeResponse(ctx context.Context, u httpx.User) (MeResponse, error) {
	profile, err := h.store.FindProfile(ctx, u.ID)
	if err != nil {
		return MeResponse{}, apperr.Internal(err)
	}
	if profile == nil {
		return MeResponse{}, apperr.Unauthorized("user no longer exists")
	}
	perms, err := h.store.PermissionsForUser(ctx, u.ID)
	if err != nil {
		return MeResponse{}, apperr.Internal(err)
	}
	isOwner := profile.Role == "OWNER"
	perm := func(module string, write bool) bool {
		if isOwner {
			return true
		}
		p, ok := perms[module]
		if !ok {
			return false
		}
		if write {
			return p.CanWrite
		}
		return p.CanRead
	}
	// NomeUsuario keeps its historical meaning (first name, falling back to the provider display
	// name); Nome/Sobrenome are the structured fields the profile form edits.
	display := profile.Name
	if profile.FirstName != nil && *profile.FirstName != "" {
		display = profile.FirstName
	}
	return MeResponse{
		Email:                     profile.Email,
		NomeUsuario:               display,
		Nome:                      profile.FirstName,
		Sobrenome:                 profile.LastName,
		Role:                      profile.Role,
		TemLogo:                   false,
		TemSenha:                  profile.HasPassword,
		MfaHabilitado:             profile.MFAEnabled,
		PermiteClientesLeitura:    perm("clients", false),
		PermiteClientesEscrita:    perm("clients", true),
		PermiteEmprestimosLeitura: perm("loans", false),
		PermiteEmprestimosEscrita: perm("loans", true),
		PermiteRelatoriosLeitura:  perm("reports", false),
		PermiteRelatoriosEscrita:  perm("reports", true),
		PermiteSaudeLeitura:       perm("health", false),
		PermiteSaudeEscrita:       perm("health", true),
		PermiteFinancasLeitura:    perm("finance", false),
		PermiteFinancasEscrita:    perm("finance", true),
	}, nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func setMFAChallengeCookie(w http.ResponseWriter, r *http.Request, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name: mfaChallengeCookie, Value: raw, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(mfaChallengeValidity.Seconds()),
		Secure: requestIsSecure(r),
	})
}

func clearMFAChallengeCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: mfaChallengeCookie, Value: "", Path: "/", MaxAge: -1})
}

// requestIsSecure mirrors httpx's own rule (which is unexported): trust TLS directly or a reverse
// proxy that terminates it.
func requestIsSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
