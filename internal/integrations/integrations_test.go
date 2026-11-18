package integrations

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	external map[Type]ExternalIntegration
	system   *SystemIntegration
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{external: make(map[Type]ExternalIntegration)}
}

func (f *fakeRepository) ListExternal(_ context.Context) ([]ExternalIntegration, error) {
	var result []ExternalIntegration
	for _, e := range f.external {
		result = append(result, e)
	}
	return result, nil
}

func (f *fakeRepository) FindExternalByType(_ context.Context, t Type) (*ExternalIntegration, error) {
	e, ok := f.external[t]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (f *fakeRepository) UpsertExternal(_ context.Context, e ExternalIntegration) error {
	f.external[e.Type] = e
	return nil
}

func (f *fakeRepository) DeleteExternalByType(_ context.Context, t Type) error {
	delete(f.external, t)
	return nil
}

func (f *fakeRepository) GetSystemStatus(_ context.Context) (*SystemIntegration, error) {
	return f.system, nil
}

func (f *fakeRepository) DeleteSystemByType(_ context.Context, t Type) error {
	if f.system != nil && f.system.Type == t {
		f.system = nil
	}
	return nil
}

func setEncryptionKey(t *testing.T) {
	t.Helper()
	// A 32-byte (AES-256) key, base64-encoded — same shape as the real APP_ENCRYPTION_KEY.
	if err := os.Setenv("APP_ENCRYPTION_KEY", "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="); err != nil {
		t.Fatalf("setting APP_ENCRYPTION_KEY: %v", err)
	}
	t.Cleanup(func() { os.Unsetenv("APP_ENCRYPTION_KEY") }) //nolint:errcheck
}

func TestService_Connect_RequiresSecretOnFirstConnect(t *testing.T) {
	setEncryptionKey(t)
	repo := newFakeRepository()
	service := NewService(repo, nil)
	ctx := context.Background()

	_, err := service.Connect(ctx, ConnectExternalIntegrationRequest{Type: TypeTelegram})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
		t.Fatalf("expected a validation apperr.Error, got %v", err)
	}
}

func TestService_Connect_ListDisconnect(t *testing.T) {
	setEncryptionKey(t)
	repo := newFakeRepository()
	service := NewService(repo, nil)
	ctx := context.Background()

	secret := "telegram-bot-token"
	created, err := service.Connect(ctx, ConnectExternalIntegrationRequest{
		Type: TypeTelegram, Secret: &secret,
	})
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}

	// The stored row must never carry the plaintext secret anywhere the DTO could leak it.
	stored := repo.external[TypeTelegram]
	if len(stored.RefreshTokenEncrypted) == 0 || string(stored.RefreshTokenEncrypted) == secret {
		t.Fatal("expected the secret to be encrypted, not stored in plaintext")
	}
	decrypted, err := Decrypt(stored.RefreshTokenEncrypted, stored.RefreshTokenIV)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if decrypted != secret {
		t.Fatalf("expected decrypted secret %q, got %q", secret, decrypted)
	}

	// Reconnecting without a secret must keep the existing encrypted credential.
	newAccount := "me@example.com"
	updated, err := service.Connect(ctx, ConnectExternalIntegrationRequest{
		Type: TypeTelegram, ConnectedAccount: &newAccount,
	})
	if err != nil {
		t.Fatalf("Connect (reconnect without secret) returned error: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("expected reconnect to update the same row, got a different id")
	}
	stillDecrypted, err := Decrypt(repo.external[TypeTelegram].RefreshTokenEncrypted, repo.external[TypeTelegram].RefreshTokenIV)
	if err != nil || stillDecrypted != secret {
		t.Fatalf("expected the original secret to survive a reconnect without a new secret, got %q, err %v", stillDecrypted, err)
	}

	list, err := service.ListExternal(ctx)
	if err != nil {
		t.Fatalf("ListExternal returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 external integration, got %d", len(list))
	}

	if err := service.DisconnectExternal(ctx, TypeTelegram); err != nil {
		t.Fatalf("DisconnectExternal returned error: %v", err)
	}
	if err := service.DisconnectExternal(ctx, TypeTelegram); !isNotFound(err) {
		t.Fatalf("expected NotFound disconnecting an already-disconnected type, got %v", err)
	}
}

func TestService_BeginGoogleDriveConnect_RequiresConfiguredOAuth(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil)

	_, _, err := service.BeginGoogleDriveConnect()

	var appErr *apperr.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected an apperr.Error when google oauth isn't configured, got %v", err)
	}
}

func TestService_BeginGoogleDriveConnect_ReturnsStateAndAuthURL(t *testing.T) {
	repo := newFakeRepository()
	googleDrive := NewGoogleDriveOAuthConfig("client-id", "client-secret", "https://example.com/callback")
	service := NewService(repo, googleDrive)

	state, authURL, err := service.BeginGoogleDriveConnect()
	if err != nil {
		t.Fatalf("BeginGoogleDriveConnect returned error: %v", err)
	}
	if state == "" {
		t.Fatal("expected a non-empty state")
	}
	if !strings.Contains(authURL, "state="+state) {
		t.Fatalf("expected authURL to embed the generated state, got %q", authURL)
	}
	if !strings.Contains(authURL, "access_type=offline") || !strings.Contains(authURL, "prompt=consent") {
		t.Fatalf("expected offline access + forced consent (so google always returns a refresh token), got %q", authURL)
	}

	state2, _, err := service.BeginGoogleDriveConnect()
	if err != nil {
		t.Fatalf("second BeginGoogleDriveConnect returned error: %v", err)
	}
	if state2 == state {
		t.Fatal("expected each call to generate a fresh, single-use state")
	}
}

func TestService_CompleteGoogleDriveConnect_RequiresConfiguredOAuth(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil)

	_, err := service.CompleteGoogleDriveConnect(context.Background(), "some-code")

	var appErr *apperr.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected an apperr.Error when google oauth isn't configured, got %v", err)
	}
}

func isNotFound(err error) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Kind == apperr.KindNotFound
}

func isValidationError(err error) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Kind == apperr.KindValidation
}
