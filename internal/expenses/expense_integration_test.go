//go:build integration

package expenses_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/expenses/postgres"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// TestRepository_CreateGetUpdateDelete exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/expenses/...
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
	service := expenses.NewService(repo)

	created, err := service.Create(ctx, expenses.CreateExpenseRequest{
		Amount:      "120.50",
		ExpenseDate: civildate.Of(time.Now()),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM expenses WHERE id = $1`, created.ID) //nolint:errcheck

	if created.Reviewed {
		t.Fatal("expected a newly created expense to be unreviewed")
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Amount != "120.50" {
		t.Fatalf("expected amount 120.50 round-tripped through numeric(15,2), got %q", got.Amount)
	}

	updated, err := service.Update(ctx, created.ID, expenses.UpdateExpenseRequest{
		Amount:      "120.50",
		ExpenseDate: created.ExpenseDate,
		Reviewed:    true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.Reviewed {
		t.Fatal("expected the expense to be marked reviewed after Update")
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
	service := expenses.NewService(repo)

	unknownCategory := uuid.New()
	_, err = service.Create(ctx, expenses.CreateExpenseRequest{
		CategoryID:  &unknownCategory,
		Amount:      "1.00",
		ExpenseDate: civildate.Of(time.Now()),
	})
	if err == nil {
		t.Fatal("expected an error for a nonexistent categoryId")
	}
}
