//go:build integration

package assets_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/assets"
	"github.com/atlas/atlas-go/internal/assets/postgres"
)

// TestRepository_CreateGetUpdateDelete exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/assets/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestRepository_CreateGetUpdateDelete(t *testing.T) {
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
	service := assets.NewService(repo)

	acquisitionValue := "45000.00"
	created, err := service.Create(ctx, assets.CreateAssetRequest{
		Name:             "Integration Test Car",
		AcquisitionValue: &acquisitionValue,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM assets WHERE id = $1`, created.ID) //nolint:errcheck

	if created.Status != assets.StatusActive {
		t.Fatalf("expected ACTIVE status, got %v", created.Status)
	}
	if len(created.Attributes) != 0 {
		t.Fatalf("expected default attributes to be empty, got %v", created.Attributes)
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AcquisitionValue == nil || *got.AcquisitionValue != "45000.00" {
		t.Fatalf("expected acquisitionValue 45000.00 round-tripped through numeric(15,2), got %v", got.AcquisitionValue)
	}

	estimatedValue := "1200.00"
	updated, err := service.Update(ctx, created.ID, assets.UpdateAssetRequest{
		Name:   "Sold Car",
		Status: assets.StatusSold,
		Attributes: []assets.AssetAttribute{
			{Name: "Engine", Type: "PART", EstimatedValue: &estimatedValue},
		},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Status != assets.StatusSold {
		t.Fatalf("expected SOLD status after update, got %v", updated.Status)
	}
	if len(updated.Attributes) != 1 || updated.Attributes[0].Name != "Engine" ||
		updated.Attributes[0].EstimatedValue == nil || *updated.Attributes[0].EstimatedValue != "1200.00" {
		t.Fatalf("expected Attributes to round-trip through jsonb, got %+v", updated.Attributes)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := service.Get(ctx, created.ID); err == nil {
		t.Fatal("expected Get to fail after Delete")
	}
}
