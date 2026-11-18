package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/captcha"
	"github.com/atlas/atlas-go/internal/platform/eventlog"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// UpdateProfile handles PUT /api/auth/perfil — the signed-in user edits their own name. No
// permission check: every authenticated user owns their own profile.
func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) error {
	u, ok := httpx.UserFromContext(r.Context())
	if !ok {
		return apperr.Unauthorized("no authenticated user in context")
	}
	req, err := httpx.DecodeAndValidate[UpdateProfileRequest](r)
	if err != nil {
		return err
	}
	nome := strings.TrimSpace(req.Nome)
	sobrenome := strings.TrimSpace(req.Sobrenome)
	if nome == "" {
		return apperr.Validation("nome is required", "nome")
	}
	if err := h.store.UpdateProfile(r.Context(), u.ID, nome, sobrenome); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ChangePassword handles PUT /api/auth/senha — sets/changes the signed-in user's own password. A
// captcha is required when Turnstile is configured (the user asked for it on this screen); the
// account password is required only when one already exists (Google-only accounts set their first).
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) error {
	u, ok := httpx.UserFromContext(r.Context())
	if !ok {
		return apperr.Unauthorized("no authenticated user in context")
	}
	req, err := httpx.DecodeAndValidate[ChangePasswordRequest](r)
	if err != nil {
		return err
	}
	if err := captcha.Verify(r.Context(), h.captchaVerifier, req.CaptchaToken, httpx.ClientIP(r)); err != nil {
		return err
	}
	current := ""
	if req.SenhaAtual != nil {
		current = *req.SenhaAtual
	}
	if err := h.store.ChangePassword(r.Context(), u.ID, current, req.NovaSenha); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// MFASetup handles POST /api/auth/mfa/setup — mints a pending TOTP secret and returns the material
// the authenticator app needs. Pending until MFAActivate proves the app already works, so a
// half-done enrollment can never lock the user out.
func (h *Handler) MFASetup(w http.ResponseWriter, r *http.Request) error {
	u, ok := httpx.UserFromContext(r.Context())
	if !ok {
		return apperr.Unauthorized("no authenticated user in context")
	}
	if !h.box.Configured() {
		return apperr.Internal(errors.New("auth: MFA cannot be enrolled without APP_ENCRYPTION_KEY"))
	}
	encrypted, enabled, err := h.store.MFASecret(r.Context(), u.ID)
	if err != nil {
		return err
	}
	if enabled && encrypted != nil {
		return apperr.Conflict("MFA is already enabled — disable it before setting up a new device")
	}
	account := u.ID.String()
	if profile, err := h.store.FindProfile(r.Context(), u.ID); err != nil {
		return apperr.Internal(err)
	} else if profile != nil {
		account = profile.Email
	}
	key, err := newTOTPKey(h.issuer, account)
	if err != nil {
		return apperr.Internal(err)
	}
	sealed, err := h.box.Seal([]byte(key.Secret()), secretAAD(u.ID.String()))
	if err != nil {
		return apperr.Internal(err)
	}
	if err := h.store.SetMFASecret(r.Context(), u.ID, sealed); err != nil {
		return err
	}
	qr, err := qrPNGBase64(key)
	if err != nil {
		return apperr.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusOK, MFASetupResponse{
		Secret:      key.Secret(),
		OtpauthURL:  key.URL(),
		QrPngBase64: qr,
	})
	return nil
}

// MFAActivate handles POST /api/auth/mfa/activate — verifies the pending secret produces valid
// codes, turns MFA on, and returns the one-time recovery codes.
func (h *Handler) MFAActivate(w http.ResponseWriter, r *http.Request) error {
	u, ok := httpx.UserFromContext(r.Context())
	if !ok {
		return apperr.Unauthorized("no authenticated user in context")
	}
	req, err := httpx.DecodeAndValidate[MFAActivateRequest](r)
	if err != nil {
		return err
	}
	secret, err := h.pendingSecret(r, u.ID)
	if err != nil {
		return err
	}
	if !validateTOTP(secret, req.Code) {
		return apperr.Validation("código inválido — confira o relógio do dispositivo e tente novamente", "code")
	}
	codes, err := newRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return apperr.Internal(err)
	}
	if err := h.store.ActivateMFA(r.Context(), u.ID, hashRecoveryCodes(codes)); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, MFARecoveryCodesResponse{RecoveryCodes: codes})
	return nil
}

// MFADisable handles POST /api/auth/mfa/disable. Possession is proven with the account password
// when one exists, otherwise with a current TOTP code.
func (h *Handler) MFADisable(w http.ResponseWriter, r *http.Request) error {
	u, ok := httpx.UserFromContext(r.Context())
	if !ok {
		return apperr.Unauthorized("no authenticated user in context")
	}
	req, err := httpx.DecodeAndValidate[MFADisableRequest](r)
	if err != nil {
		return err
	}
	profile, err := h.store.FindProfile(r.Context(), u.ID)
	if err != nil {
		return apperr.Internal(err)
	}
	if profile == nil || !profile.MFAEnabled {
		return apperr.Conflict("MFA is not enabled")
	}
	if profile.HasPassword {
		current := ""
		if req.SenhaAtual != nil {
			current = *req.SenhaAtual
		}
		valid, err := h.store.CheckPassword(r.Context(), u.ID, current)
		if err != nil {
			return apperr.Internal(err)
		}
		if !valid {
			return apperr.Unauthorized("current password is incorrect")
		}
	} else {
		secret, err := h.activeSecret(r, u.ID)
		if err != nil {
			return err
		}
		if !validateTOTP(secret, req.Code) {
			return apperr.Validation("código inválido", "code")
		}
	}
	if err := h.store.DisableMFA(r.Context(), u.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// MFARegenerateRecoveryCodes handles POST /api/auth/mfa/recovery-codes — replaces the recovery-code
// set after proving possession with a current TOTP code. Existing codes stop working immediately.
func (h *Handler) MFARegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) error {
	u, ok := httpx.UserFromContext(r.Context())
	if !ok {
		return apperr.Unauthorized("no authenticated user in context")
	}
	req, err := httpx.DecodeAndValidate[MFARegenerateRequest](r)
	if err != nil {
		return err
	}
	secret, err := h.activeSecret(r, u.ID)
	if err != nil {
		return err
	}
	if !validateTOTP(secret, req.Code) {
		return apperr.Validation("código inválido", "code")
	}
	codes, err := newRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return apperr.Internal(err)
	}
	if err := h.store.ReplaceRecoveryCodes(r.Context(), u.ID, hashRecoveryCodes(codes)); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, MFARecoveryCodesResponse{RecoveryCodes: codes})
	return nil
}

// LoginMFA handles POST /api/auth/login/mfa — completes a login that Login paused for the second
// factor. A TOTP code or an unused recovery code both work; the challenge is consumed only on
// success, and the endpoint is rate-limited.
func (h *Handler) LoginMFA(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[MFAVerifyRequest](r)
	if err != nil {
		return err
	}
	// The challenge normally arrives in the body (password login), but a Google-OAuth login started
	// by a full-page redirect delivers it in an HttpOnly cookie instead.
	rawToken := req.MFAToken
	if strings.TrimSpace(rawToken) == "" {
		if c, err := r.Cookie(mfaChallengeCookie); err == nil {
			rawToken = c.Value
		}
	}
	if strings.TrimSpace(rawToken) == "" {
		return apperr.Unauthorized("missing verification challenge — sign in again")
	}
	challenge, err := h.store.FindMFAChallenge(r.Context(), rawToken)
	if err != nil {
		return err
	}
	if challenge == nil {
		return apperr.Unauthorized("invalid or expired verification challenge — sign in again")
	}
	secret, err := h.activeSecretFor(r, challenge.AppUserID)
	if err != nil {
		return err
	}
	verified := validateTOTP(secret, req.Code)
	if !verified {
		used, err := h.store.ConsumeRecoveryCode(r.Context(), challenge.AppUserID, HashRecoveryCode(req.Code))
		if err != nil {
			return err
		}
		verified = used
	}
	if !verified {
		uid := challenge.AppUserID
		h.recordAuth(r, eventlog.TypeLoginFailure, eventlog.SeverityWarning, &uid, "", "Código de verificação inválido")
		return apperr.Unauthorized("invalid verification code")
	}
	if err := h.store.MarkMFAChallengeUsed(r.Context(), challenge.ID); err != nil {
		return err
	}
	clearMFAChallengeCookie(w)
	token, expiresAt, err := h.store.CreateSession(r.Context(), challenge.AppUserID)
	if err != nil {
		return err
	}
	httpx.SetSessionCookie(w, r, token, expiresAt)
	uid := challenge.AppUserID
	h.recordAuth(r, eventlog.TypeLoginSuccess, eventlog.SeverityInfo, &uid, "", "Login com segundo fator")
	return h.writeLoginDone(w, r.Context(), token)
}

// pendingSecret decrypts the enrollment secret stored by MFASetup (MFA not yet enabled).
func (h *Handler) pendingSecret(r *http.Request, userID uuid.UUID) (string, error) {
	if !h.box.Configured() {
		return "", apperr.Internal(errors.New("auth: MFA is not configured on this server"))
	}
	encrypted, enabled, err := h.store.MFASecret(r.Context(), userID)
	if err != nil {
		return "", err
	}
	if encrypted == nil || enabled {
		return "", apperr.Conflict("no MFA enrollment in progress — start setup first")
	}
	return h.openSecret(userID, encrypted)
}

// activeSecret decrypts the enrollment secret for a user with MFA enabled.
func (h *Handler) activeSecret(r *http.Request, userID uuid.UUID) (string, error) {
	return h.activeSecretFor(r, userID)
}

func (h *Handler) activeSecretFor(r *http.Request, userID uuid.UUID) (string, error) {
	if !h.box.Configured() {
		return "", apperr.Internal(errors.New("auth: MFA is not configured on this server"))
	}
	encrypted, enabled, err := h.store.MFASecret(r.Context(), userID)
	if err != nil {
		return "", err
	}
	if encrypted == nil || !enabled {
		return "", apperr.Unauthorized("MFA is not active for this account")
	}
	return h.openSecret(userID, encrypted)
}

func (h *Handler) openSecret(userID uuid.UUID, encrypted []byte) (string, error) {
	raw, err := h.box.Open(encrypted, secretAAD(userID.String()))
	if err != nil {
		return "", apperr.Internal(err)
	}
	return string(raw), nil
}

func hashRecoveryCodes(codes []string) []string {
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = HashRecoveryCode(c)
	}
	return hashes
}
