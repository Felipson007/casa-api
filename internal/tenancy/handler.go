package tenancy

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
	"github.com/atlas/atlas-go/internal/platform/logging"
)

// SessionIssuer issues a session for a freshly-created user. Satisfied by auth.Store — kept as a
// narrow interface here so tenancy does not depend on the whole auth surface.
type SessionIssuer interface {
	LoginWithPassword(ctx context.Context, email, password string) (rawToken string, expiresAt time.Time, err error)
}

// Config gates the self-service route. Disabled means the route is not even registered.
type Config struct {
	Enabled bool
	// InviteToken, when non-empty, must be supplied in the signup body — a light gate for a
	// private/homelab deployment that is not otherwise open for public registration.
	InviteToken string
}

// Handler serves the self-service signup endpoint.
type Handler struct {
	provisioner *Provisioner
	issuer      SessionIssuer
	cfg         Config
}

func NewHandler(provisioner *Provisioner, issuer SessionIssuer, cfg Config) *Handler {
	return &Handler{provisioner: provisioner, issuer: issuer, cfg: cfg}
}

// SignupRequest is the self-service registration body. The password field keeps the same
// Portuguese name as the login DTO (`senha`) so the frontend can reuse its form shape.
type SignupRequest struct {
	Slug        string `json:"slug" validate:"required"`
	Email       string `json:"email" validate:"required,email"`
	Senha       string `json:"senha" validate:"required,min=8"`
	Nome        string `json:"nome"`
	InviteToken string `json:"inviteToken"`
}

// SignupResponse is returned after the tenant schema and its OWNER user are created; the session
// cookie is already set, so the client is logged in.
type SignupResponse struct {
	Email      string `json:"email"`
	TenantSlug string `json:"tenantSlug"`
	SchemaName string `json:"schemaName"`
}

// Signup handles POST /api/auth/signup. It provisions a brand-new tenant (own schema + tables) and
// its OWNER user, then logs that user in. The route is only registered when Config.Enabled.
func (h *Handler) Signup(w http.ResponseWriter, r *http.Request) error {
	if !h.cfg.Enabled {
		return apperr.NotFound("signup is not enabled")
	}
	req, err := httpx.DecodeAndValidate[SignupRequest](r)
	if err != nil {
		return err
	}
	if h.cfg.InviteToken != "" &&
		subtle.ConstantTimeCompare([]byte(req.InviteToken), []byte(h.cfg.InviteToken)) != 1 {
		return apperr.Forbidden("invalid invite token")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Senha), bcrypt.DefaultCost)
	if err != nil {
		return apperr.Internal(err)
	}

	tenant, _, err := h.provisioner.Provision(r.Context(), ProvisionRequest{
		Slug:         req.Slug,
		DisplayName:  req.Nome,
		Email:        req.Email,
		Name:         req.Nome,
		PasswordHash: string(hash),
	})
	if err != nil {
		return err
	}

	token, expiresAt, err := h.issuer.LoginWithPassword(r.Context(), req.Email, req.Senha)
	if err != nil {
		return err
	}
	logging.FromContext(r.Context()).Info("tenancy: tenant provisioned",
		"tenant_slug", tenant.Slug, "schema", tenant.SchemaName, "email", req.Email)
	httpx.SetSessionCookie(w, r, token, expiresAt)
	httpx.WriteJSON(w, http.StatusCreated, SignupResponse{
		Email:      req.Email,
		TenantSlug: tenant.Slug,
		SchemaName: tenant.SchemaName,
	})
	return nil
}
