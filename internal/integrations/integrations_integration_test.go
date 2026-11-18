//go:build integration

package integrations_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/integrations"
	"github.com/atlas/atlas-go/internal/integrations/postgres"
)

const testEncryptionKey = "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=" // 32 raw bytes, base64

// TestRepository_ConnectListDisconnect exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/integrations/...
//
// against TEST_DATABASE_URL (never DATABASE_URL). Requires 000002_fix_integration_types (D-013)
// to be applied — the original 000001 CHECK would reject GEMINI/DEEPSEEK/GROQ/OPENROUTER outright.
func TestRepository_ConnectListDisconnect(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if err := os.Setenv("APP_ENCRYPTION_KEY", testEncryptionKey); err != nil {
		t.Fatalf("setting APP_ENCRYPTION_KEY: %v", err)
	}
	defer os.Unsetenv("APP_ENCRYPTION_KEY") //nolint:errcheck

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()

	repo := postgres.NewRepository(pool)
	service := integrations.NewService(repo, nil)

	secret := "telegram-bot-token"
	created, err := service.Connect(ctx, integrations.ConnectExternalIntegrationRequest{
		Type: integrations.TypeTelegram, Secret: &secret,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM external_integrations WHERE id = $1`, created.ID) //nolint:errcheck

	list, err := service.ListExternal(ctx)
	if err != nil {
		t.Fatalf("ListExternal: %v", err)
	}
	found := false
	for _, e := range list {
		if e.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the created integration to appear in ListExternal")
	}

	if err := service.DisconnectExternal(ctx, integrations.TypeTelegram); err != nil {
		t.Fatalf("DisconnectExternal: %v", err)
	}
	if err := service.DisconnectExternal(ctx, integrations.TypeTelegram); err == nil {
		t.Fatal("expected DisconnectExternal to fail after it's already disconnected")
	}
}
