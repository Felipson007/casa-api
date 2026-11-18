package auth

// LoginRequest matches the existing frontend's authApi.login(email, senha) body exactly (the
// frontend's login page/form is otherwise unchanged) — the one place in this rewrite that keeps a
// Portuguese field name on purpose, to avoid touching an already-built, working UI for a rename
// with no functional benefit.
type LoginRequest struct {
	Email string `json:"email" validate:"required,email"`
	Senha string `json:"senha" validate:"required"`
}

// MeResponse is the authenticated person's identity and module permissions. The application is
// household-scoped, so this contract deliberately contains no organization/tenant placeholder.
type MeResponse struct {
	Email string `json:"email"`
	// NomeUsuario stays for existing readers: the user's first name (or, for Google-only accounts
	// that never set one, the display name Google provided).
	NomeUsuario *string `json:"nomeUsuario"`
	// Nome/Sobrenome are the structured, editable name fields (see UpdateProfileRequest).
	Nome      *string `json:"nome"`
	Sobrenome *string `json:"sobrenome"`
	Role      string  `json:"role"`
	TemLogo   bool    `json:"temLogo"`
	TemSenha  bool    `json:"temSenha"`
	// MfaHabilitado tells the profile UI whether the second factor is already active.
	MfaHabilitado             bool `json:"mfaHabilitado"`
	PermiteClientesLeitura    bool `json:"permiteClientesLeitura"`
	PermiteClientesEscrita    bool `json:"permiteClientesEscrita"`
	PermiteEmprestimosLeitura bool `json:"permiteEmprestimosLeitura"`
	PermiteEmprestimosEscrita bool `json:"permiteEmprestimosEscrita"`
	PermiteRelatoriosLeitura  bool `json:"permiteRelatoriosLeitura"`
	PermiteRelatoriosEscrita  bool `json:"permiteRelatoriosEscrita"`
	PermiteSaudeLeitura       bool `json:"permiteSaudeLeitura"`
	PermiteSaudeEscrita       bool `json:"permiteSaudeEscrita"`
	PermiteFinancasLeitura    bool `json:"permiteFinancasLeitura"`
	PermiteFinancasEscrita    bool `json:"permiteFinancasEscrita"`
}

// LoginResponse is either a completed login (Me populated) or a request for the second factor
// (MFARequired + MFAToken). It is deliberately one shape so the frontend has a single branch to
// make: never a session in the MFA-required response.
type LoginResponse struct {
	MFARequired bool        `json:"mfaRequired"`
	MFAToken    string      `json:"mfaToken,omitempty"`
	Me          *MeResponse `json:"me,omitempty"`
}

// MFAVerifyRequest completes a login started by Login: the challenge token it returned plus a TOTP
// code or one of the single-use recovery codes.
// MFAToken may be empty when the challenge travelled in the HttpOnly cookie (Google OAuth login).
type MFAVerifyRequest struct {
	MFAToken string `json:"mfaToken"`
	Code     string `json:"code" validate:"required"`
}

// UpdateProfileRequest edits the signed-in user's structured name.
type UpdateProfileRequest struct {
	Nome      string `json:"nome" validate:"required,max=100"`
	Sobrenome string `json:"sobrenome" validate:"max=100"`
}

// ChangePasswordRequest sets a new password for the signed-in user. SenhaAtual is required only
// when the account already has a password; CaptchaToken is required when the server has Turnstile
// configured.
type ChangePasswordRequest struct {
	SenhaAtual   *string `json:"senhaAtual"`
	NovaSenha    string  `json:"novaSenha" validate:"required,min=8,max=72"`
	CaptchaToken string  `json:"captchaToken"`
}

// MFASetupResponse carries the enrollment material to the authenticator app — the base32 secret,
// the otpauth:// URL, and a ready-to-render QR PNG.
type MFASetupResponse struct {
	Secret      string `json:"secret"`
	OtpauthURL  string `json:"otpauthUrl"`
	QrPngBase64 string `json:"qrPngBase64"`
}

// MFAActivateRequest confirms enrollment by proving the authenticator already produces valid codes.
type MFAActivateRequest struct {
	Code string `json:"code" validate:"required"`
}

// MFARecoveryCodesResponse returns the plaintext recovery codes exactly once, at activation or
// regeneration time — only their hashes are ever stored.
type MFARecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recoveryCodes"`
}

// MFADisableRequest proves possession before turning the second factor off: the account password
// when one exists, otherwise a current TOTP code.
type MFADisableRequest struct {
	SenhaAtual *string `json:"senhaAtual"`
	Code       string  `json:"code"`
}

// MFARegenerateRequest proves possession with a current TOTP code before minting a new set.
type MFARegenerateRequest struct {
	Code string `json:"code" validate:"required"`
}
