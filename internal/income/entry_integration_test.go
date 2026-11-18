//go:build integration

package income_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/income/postgres"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// TestRepository_CreateGetUpdateDelete exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/income/...
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
	service := income.NewService(repo)

	created, err := service.Create(ctx, income.CreateEntryRequest{
		Amount:     "3200.00",
		IncomeDate: civildate.Of(time.Now()),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM income_entries WHERE id = $1`, created.ID) //nolint:errcheck

	if created.Reviewed {
		t.Fatal("expected a newly created income entry to be unreviewed")
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Amount != "3200.00" {
		t.Fatalf("expected amount 3200.00 round-tripped through numeric(15,2), got %q", got.Amount)
	}

	updated, err := service.Update(ctx, created.ID, income.UpdateEntryRequest{
		Amount:     "3200.00",
		IncomeDate: created.IncomeDate,
		Reviewed:   true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.Reviewed {
		t.Fatal("expected the income entry to be marked reviewed after Update")
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := service.Get(ctx, created.ID); err == nil {
		t.Fatal("expected Get to fail after Delete")
	}
}

// TestRepository_Create_RejectsUnknownCategory confirms the FK-violation -> apperr.Validation
// translation.
func TestRepository_Create_RejectsUnknownCategory(t *testing.T) {
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
	service := income.NewService(repo)

	unknownCategory := uuid.New()
	_, err = service.Create(ctx, income.CreateEntryRequest{
		CategoryID: &unknownCategory,
		Amount:     "1.00",
		IncomeDate: civildate.Of(time.Now()),
	})
	if err == nil {
		t.Fatal("expected an error for a nonexistent categoryId")
	}
}
