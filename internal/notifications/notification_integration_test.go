//go:build integration

package notifications_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/notifications"
	"github.com/atlas/atlas-go/internal/notifications/postgres"
)

// TestRepository_CreateGetMarkReadDelete exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/notifications/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestRepository_CreateGetMarkReadDelete(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()

	repo := postgres.NewRepository(pool)
	service := notifications.NewService(repo)

	created, err := service.Create(ctx, notifications.CreateNotificationRequest{
		Type: notifications.NotificationTypeInstallmentDueSoon, Title: "Integration Test", Message: "hello",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM notifications WHERE id = $1`, created.ID) //nolint:errcheck

	if created.Read {
		t.Fatal("expected a newly created notification to be unread")
	}

	if err := service.MarkRead(ctx, created.ID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Read {
		t.Fatal("expected notification to be read after MarkRead")
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := service.Get(ctx, created.ID); err == nil {
		t.Fatal("expected Get to fail after Delete")
	}
}

// TestRepository_Configuration_LazyProvisionsAndUpdates confirms the singleton-provisioning
// behavior against a real Postgres. Cleans up any configuration row(s) it creates.
func TestRepository_Configuration_LazyProvisionsAndUpdates(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()
	defer pool.Exec(ctx, `DELETE FROM notification_configuration`) //nolint:errcheck

	repo := postgres.NewRepository(pool)
	service := notifications.NewService(repo)

	config, err := service.GetConfiguration(ctx)
	if err != nil {
		t.Fatalf("GetConfiguration: %v", err)
	}
	if config.RetentionDays != 7 {
		t.Fatalf("expected default retention days 7, got %d", config.RetentionDays)
	}

	updated, err := service.UpdateConfiguration(ctx, notifications.UpdateConfigurationRequest{RetentionDays: 30})
	if err != nil {
		t.Fatalf("UpdateConfiguration: %v", err)
	}
	if updated.RetentionDays != 30 || updated.ID != config.ID {
		t.Fatalf("expected the same row updated to retentionDays=30, got %+v", updated)
	}
}
