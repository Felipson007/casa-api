package finance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	balances map[uuid.UUID]Balance
	history  map[uuid.UUID][]BalanceHistoryEntry
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		balances: make(map[uuid.UUID]Balance),
		history:  make(map[uuid.UUID][]BalanceHistoryEntry),
	}
}

func (f *fakeRepository) List(_ context.Context) ([]Balance, error) {
	var result []Balance
	for _, b := range f.balances {
		result = append(result, b)
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Balance, error) {
	b, ok := f.balances[id]
	if !ok {
		return nil, nil
	}
	return &b, nil
}

func (f *fakeRepository) Create(_ context.Context, b Balance) error {
	f.balances[b.ID] = b
	return nil
}

func (f *fakeRepository) Update(_ context.Context, b Balance, snapshot *BalanceHistoryEntry) error {
	if _, ok := f.balances[b.ID]; !ok {
		return errors.New("not found")
	}
	if snapshot != nil {
		f.history[snapshot.BalanceID] = append(f.history[snapshot.BalanceID], *snapshot)
	}
	f.balances[b.ID] = b
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.balances, id)
	delete(f.history, id)
	return nil
}

func (f *fakeRepository) ListHistory(_ context.Context, balanceID uuid.UUID) ([]BalanceHistoryEntry, error) {
	return f.history[balanceID], nil
}

func TestService_CreateListDelete(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateBalanceRequest{
		Name: "FGTS", Type: BalanceTypeFGTS, CurrentValue: "1000.00", UpdatedOn: civildate.Of(time.Now()),
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	list, err := service.List(ctx)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 balance, got %d", len(list))
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := service.ListHistory(ctx, created.ID); !isNotFound(err) {
		t.Fatalf("expected NotFound after Delete, got %v", err)
	}
}

func TestService_Update_SnapshotsOnlyWhenValueOrDateChanges(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	updatedOn := civildate.Of(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	created, err := service.Create(ctx, CreateBalanceRequest{
		Name: "FGTS", Type: BalanceTypeFGTS, CurrentValue: "1000.00", UpdatedOn: updatedOn,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	// Editing only Name (same CurrentValue string and same UpdatedOn) must NOT create a history
	// entry.
	if _, err := service.Update(ctx, created.ID, UpdateBalanceRequest{
		Name: "FGTS (renamed)", Type: BalanceTypeFGTS, CurrentValue: "1000.00", UpdatedOn: updatedOn,
	}); err != nil {
		t.Fatalf("Update (rename only) returned error: %v", err)
	}
	history, err := service.ListHistory(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListHistory returned error: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("expected no history entry from a no-op value edit, got %d", len(history))
	}

	// Actually changing CurrentValue must snapshot the PREVIOUS value.
	newUpdatedOn := civildate.Of(updatedOn.Time().AddDate(0, 1, 0))
	updated, err := service.Update(ctx, created.ID, UpdateBalanceRequest{
		Name: "FGTS (renamed)", Type: BalanceTypeFGTS, CurrentValue: "1200.00", UpdatedOn: newUpdatedOn,
	})
	if err != nil {
		t.Fatalf("Update (value change) returned error: %v", err)
	}
	if updated.CurrentValue != "1200.00" {
		t.Fatalf("expected updated CurrentValue 1200.00, got %s", updated.CurrentValue)
	}
	history, err = service.ListHistory(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListHistory returned error: %v", err)
	}
	if len(history) != 1 || history[0].Balance != "1000.00" {
		t.Fatalf("expected 1 history entry snapshotting the previous value 1000.00, got %+v", history)
	}
}

// TestService_Update_NumericallyEqualValue_NoSnapshot confirms amountsDiffer compares real
// numeric value, not string equality — "1000" and "1000.00" are the same amount.
func TestService_Update_NumericallyEqualValue_NoSnapshot(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	updatedOn := civildate.Of(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	created, err := service.Create(ctx, CreateBalanceRequest{
		Name: "FGTS", Type: BalanceTypeFGTS, CurrentValue: "1000.00", UpdatedOn: updatedOn,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if _, err := service.Update(ctx, created.ID, UpdateBalanceRequest{
		Name: "FGTS", Type: BalanceTypeFGTS, CurrentValue: "1000", UpdatedOn: updatedOn,
	}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	history, err := service.ListHistory(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListHistory returned error: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("expected no history entry for a numerically-equal value (1000 == 1000.00), got %d", len(history))
	}
}

func isNotFound(err error) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Kind == apperr.KindNotFound
}
