package integrations

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	ListExternal(ctx context.Context) ([]ExternalIntegration, error)
	FindExternalByType(ctx context.Context, t Type) (*ExternalIntegration, error)
	// UpsertExternal inserts a new row, or updates the existing one for the same Type — mirrors
	// the old Java's findOneByTipo().orElseGet(new) + save() pattern (no DB-level UNIQUE(type); the
	// single-row-per-type guarantee is enforced here, at the application layer, same as Java).
	UpsertExternal(ctx context.Context, e ExternalIntegration) error
	DeleteExternalByType(ctx context.Context, t Type) error

	// GetSystemStatus returns nil (not an error) if nothing is connected — mirrors the Java
	// Optional<IntegracaoSistemaDto>.
	GetSystemStatus(ctx context.Context) (*SystemIntegration, error)
	DeleteSystemByType(ctx context.Context, t Type) error
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13). googleDrive is nil-safe (BeginGoogleDriveConnect/
// CompleteGoogleDriveConnect return a clear error rather than panicking) so callers that never
// exercise the Drive-connect flow don't need real credentials configured.
type Service struct {
	repo           Repository
	googleDrive    *GoogleDriveOAuthConfig
	googleCalendar *GoogleCalendarOAuthConfig
}

func NewService(repo Repository, googleDrive *GoogleDriveOAuthConfig) *Service {
	return &Service{repo: repo, googleDrive: googleDrive}
}

// WithGoogleCalendar attaches the Calendar OAuth config after construction, so existing callers
// (and the worker, which never runs the OAuth flow) don't need it.
func (s *Service) WithGoogleCalendar(cfg *GoogleCalendarOAuthConfig) *Service {
	s.googleCalendar = cfg
	return s
}

// ListExternal returns every connected external integration's status (never the secret).
func (s *Service) ListExternal(ctx context.Context) ([]ExternalIntegrationDTO, error) {
	list, err := s.repo.ListExternal(ctx)
	if err != nil {
		return nil, fmt.Errorf("integrations: listing external: %w", err)
	}
	return toExternalDTOs(list), nil
}

// Connect creates the household's connection for req.Type, or updates the existing one (same
// Type — same UNIQUE(type) semantics as the Java, enforced here rather than by the DB). Secret is
// required the first time; on a later reconnect it may be omitted to keep the currently-stored
// credential and only change the other fields — mirrors GeminiIntegracaoService.conectar exactly.
func (s *Service) Connect(ctx context.Context, req ConnectExternalIntegrationRequest) (ExternalIntegrationDTO, error) {
	existing, err := s.repo.FindExternalByType(ctx, req.Type)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: looking up existing connection: %w", err)
	}

	e := ExternalIntegration{
		Type:             req.Type,
		ConnectedAccount: req.ConnectedAccount,
		Models:           req.Models,
	}
	if existing != nil {
		e.ID = existing.ID
		e.LastTelegramUpdateID = existing.LastTelegramUpdateID
		e.TelegramPairingCode = existing.TelegramPairingCode
		e.TelegramPairingExpiresAt = existing.TelegramPairingExpiresAt
	} else {
		e.ID = uuid.New()
	}

	secretProvided := req.Secret != nil && *req.Secret != ""
	switch {
	case secretProvided:
		encrypted, err := Encrypt(*req.Secret)
		if err != nil {
			return ExternalIntegrationDTO{}, apperr.Internal(err)
		}
		e.RefreshTokenEncrypted = encrypted.Ciphertext
		e.RefreshTokenIV = encrypted.IV
	case existing != nil:
		e.RefreshTokenEncrypted = existing.RefreshTokenEncrypted
		e.RefreshTokenIV = existing.RefreshTokenIV
	default:
		return ExternalIntegrationDTO{}, apperr.Validation("secret is required to connect for the first time", "secret")
	}

	if err := s.repo.UpsertExternal(ctx, e); err != nil {
		return ExternalIntegrationDTO{}, err
	}
	// Re-read rather than mapping the in-memory `e` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above on a first connect (found systemically across every Create
	// in this codebase by Hermes's HTTP endpoint sweep on loans, msg 83 F-3).
	persisted, err := s.repo.FindExternalByType(ctx, req.Type)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: re-reading after connect: %w", err)
	}
	return toExternalDTO(*persisted), nil
}

// BeginGoogleDriveConnect generates a fresh OAuth state and returns it alongside the URL to
// redirect the browser to — mirrors GoogleDriveIntegracaoService.construirUrlAutorizacao. The
// caller (Handler) is responsible for storing state somewhere it can verify against the callback
// (a short-lived cookie here, a server-side HTTP session in the Java version).
func (s *Service) BeginGoogleDriveConnect() (state, authURL string, err error) {
	if !s.googleDrive.Enabled() {
		return "", "", apperr.Internal(fmt.Errorf("integrations: google oauth is not configured"))
	}
	state, err = generateOAuthState()
	if err != nil {
		return "", "", err
	}
	return state, s.googleDrive.AuthCodeURL(state), nil
}

// CompleteGoogleDriveConnect finishes the flow: exchanges code for tokens, resolves the connected
// account, encrypts the refresh token, and upserts the household's single GOOGLE_DRIVE row —
// mirrors GoogleDriveIntegracaoService.processarCallback's second half exactly (state validation
// itself already happened in the Handler by the time this is called).
func (s *Service) CompleteGoogleDriveConnect(ctx context.Context, code string) (ExternalIntegrationDTO, error) {
	if !s.googleDrive.Enabled() {
		return ExternalIntegrationDTO{}, apperr.Internal(fmt.Errorf("integrations: google oauth is not configured"))
	}
	refreshToken, email, err := s.googleDrive.ExchangeAndFetchAccount(ctx, code)
	if err != nil {
		return ExternalIntegrationDTO{}, err
	}

	encrypted, err := Encrypt(refreshToken)
	if err != nil {
		return ExternalIntegrationDTO{}, apperr.Internal(err)
	}

	existing, err := s.repo.FindExternalByType(ctx, TypeGoogleDrive)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: looking up existing google drive connection: %w", err)
	}
	e := ExternalIntegration{
		Type: TypeGoogleDrive, ConnectedAccount: &email,
		RefreshTokenEncrypted: encrypted.Ciphertext, RefreshTokenIV: encrypted.IV,
	}
	if existing != nil {
		e.ID = existing.ID
	} else {
		e.ID = uuid.New()
	}

	if err := s.repo.UpsertExternal(ctx, e); err != nil {
		return ExternalIntegrationDTO{}, err
	}
	persisted, err := s.repo.FindExternalByType(ctx, TypeGoogleDrive)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: re-reading after google drive connect: %w", err)
	}
	return toExternalDTO(*persisted), nil
}

// GoogleDriveRefreshToken returns the decrypted refresh token for the connected Google Drive
// integration, or apperr.NotFound if it isn't connected — the one place outside this package that
// needs the raw credential (internal/documents, to fetch a googledrive:-backed file's bytes).
// Kept here rather than exposing RefreshTokenEncrypted/RefreshTokenIV to callers, so Decrypt stays
// paired with the row it belongs to.
func (s *Service) GoogleDriveRefreshToken(ctx context.Context) (string, error) {
	existing, err := s.repo.FindExternalByType(ctx, TypeGoogleDrive)
	if err != nil {
		return "", fmt.Errorf("integrations: looking up google drive connection: %w", err)
	}
	if existing == nil {
		return "", apperr.NotFound("google drive is not connected")
	}
	token, err := Decrypt(existing.RefreshTokenEncrypted, existing.RefreshTokenIV)
	if err != nil {
		return "", fmt.Errorf("integrations: decrypting google drive refresh token: %w", err)
	}
	return token, nil
}

// DisconnectExternal removes the household's connection for t, or returns apperr.NotFound if it
// isn't currently connected.
func (s *Service) DisconnectExternal(ctx context.Context, t Type) error {
	existing, err := s.repo.FindExternalByType(ctx, t)
	if err != nil {
		return fmt.Errorf("integrations: looking up for disconnect: %w", err)
	}
	if existing == nil {
		return apperr.NotFound(fmt.Sprintf("%s is not connected", t))
	}
	if err := s.repo.DeleteExternalByType(ctx, t); err != nil {
		return fmt.Errorf("integrations: disconnecting: %w", err)
	}
	return nil
}

// SystemStatus returns the system-wide connection's status, or nil if nothing is connected — this
// module can't create one (see package doc); it can only report/disconnect a row the real Java
// app (or a future OAuth implementation) already created.
func (s *Service) SystemStatus(ctx context.Context) (*SystemIntegrationDTO, error) {
	status, err := s.repo.GetSystemStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("integrations: getting system status: %w", err)
	}
	if status == nil {
		return nil, nil
	}
	dto := toSystemDTO(*status)
	return &dto, nil
}

// DisconnectSystem removes the system-wide connection for t, or returns apperr.NotFound if it
// isn't currently connected.
func (s *Service) DisconnectSystem(ctx context.Context, t Type) error {
	status, err := s.repo.GetSystemStatus(ctx)
	if err != nil {
		return fmt.Errorf("integrations: looking up system connection for disconnect: %w", err)
	}
	if status == nil || status.Type != t {
		return apperr.NotFound(fmt.Sprintf("%s is not connected system-wide", t))
	}
	if err := s.repo.DeleteSystemByType(ctx, t); err != nil {
		return fmt.Errorf("integrations: disconnecting system integration: %w", err)
	}
	return nil
}
