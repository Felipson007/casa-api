//go:build integration

package finance_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/finance"
	"github.com/atlas/atlas-go/internal/finance/postgres"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// TestRepository_CreateUpdateHistoryDelete exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/finance/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestRepository_CreateUpdateHistoryDelete(t *testing.T) {
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
	service := finance.NewService(repo)

	updatedOn := civildate.Of(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	created, err := service.Create(ctx, finance.CreateBalanceRequest{
		Name: "Integration Test FGTS", Type: finance.BalanceTypeFGTS, CurrentValue: "1000.00", UpdatedOn: updatedOn,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM financial_balances WHERE id = $1`, created.ID) //nolint:errcheck

	newUpdatedOn := civildate.Of(updatedOn.Time().AddDate(0, 1, 0))
	updated, err := service.Update(ctx, created.ID, finance.UpdateBalanceRequest{
		Name: "Integration Test FGTS", Type: finance.BalanceTypeFGTS, CurrentValue: "1200.00", UpdatedOn: newUpdatedOn,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.CurrentValue != "1200.00" {
		t.Fatalf("expected currentValue 1200.00 round-tripped through numeric(15,2), got %s", updated.CurrentValue)
	}

	history, err := service.ListHistory(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(history) != 1 || history[0].Balance != "1000.00" {
		t.Fatalf("expected 1 history entry with the previous value 1000.00, got %+v", history)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// financial_balance_history rows must cascade-delete with their balance.
	if _, err := service.ListHistory(ctx, created.ID); err == nil {
		t.Fatal("expected ListHistory to fail after Delete (balance no longer exists)")
	}
}
