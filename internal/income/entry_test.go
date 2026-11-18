package income

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/categorize"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	byID          map[uuid.UUID]Entry
	categoryNames map[uuid.UUID]string
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byID: make(map[uuid.UUID]Entry), categoryNames: make(map[uuid.UUID]string)}
}

func (f *fakeRepository) List(_ context.Context, categoryID, personID *uuid.UUID, query string) ([]Entry, error) {
	var result []Entry
	for _, e := range f.byID {
		if categoryID != nil && (e.CategoryID == nil || *e.CategoryID != *categoryID) {
			continue
		}
		if personID != nil && (e.PersonID == nil || *e.PersonID != *personID) {
			continue
		}
		if query != "" && (e.Description == nil || !strings.Contains(strings.ToLower(*e.Description), strings.ToLower(query))) {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Entry, error) {
	e, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (f *fakeRepository) Create(_ context.Context, e Entry) error {
	f.byID[e.ID] = e
	return nil
}

func (f *fakeRepository) Update(_ context.Context, e Entry) error {
	if _, ok := f.byID[e.ID]; !ok {
		return errors.New("not found")
	}
	f.byID[e.ID] = e
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.byID, id)
	return nil
}

func (f *fakeRepository) ListByDocument(_ context.Context, documentID uuid.UUID) ([]Entry, error) {
	var result []Entry
	for _, e := range f.byID {
		if e.DocumentID != nil && *e.DocumentID == documentID {
			result = append(result, e)
		}
	}
	return result, nil
}

func (f *fakeRepository) ReplaceForDocument(_ context.Context, documentID uuid.UUID, es []Entry) error {
	for id, e := range f.byID {
		if e.DocumentID != nil && *e.DocumentID == documentID {
			delete(f.byID, id)
		}
	}
	for _, e := range es {
		f.byID[e.ID] = e
	}
	return nil
}

func (f *fakeRepository) ListInRange(_ context.Context, from, toExclusive time.Time) ([]EntryWithCategoryName, error) {
	var result []EntryWithCategoryName
	for _, e := range f.byID {
		if !e.IncomeDate.Before(from) && e.IncomeDate.Before(toExclusive) {
			result = append(result, EntryWithCategoryName{Entry: e, CategoryName: "Sem categoria"})
		}
	}
	return result, nil
}

func (f *fakeRepository) CategoryHistory(context.Context) ([]categorize.Example, error) {
	var result []categorize.Example
	for _, e := range f.byID {
		if e.CategoryID == nil || e.Description == nil || *e.Description == "" {
			continue
		}
		name := f.categoryNames[*e.CategoryID]
		if name == "" {
			continue
		}
		result = append(result, categorize.Example{
			Description: *e.Description,
			Category:    name,
			Manual:      e.CategorySource == CategorySourceManual,
		})
	}
	return result, nil
}

func (f *fakeRepository) CategoryIDsByName(context.Context, string) (map[string]uuid.UUID, error) {
	result := map[string]uuid.UUID{}
	for id, name := range f.categoryNames {
		result[strings.ToLower(name)] = id
	}
	return result, nil
}

func (f *fakeRepository) AutoCategorize(_ context.Context, assigns []CategoryAssignment) (int, error) {
	updated := 0
	for _, a := range assigns {
		e, ok := f.byID[a.ID]
		if !ok || e.CategorySource == CategorySourceManual {
			continue
		}
		categoryID := a.CategoryID
		e.CategoryID = &categoryID
		e.CategorySource = CategorySourceHistory
		f.byID[a.ID] = e
		updated++
	}
	return updated, nil
}

func TestService_CreateGetList(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	personID := uuid.New()

	created, err := service.Create(ctx, CreateEntryRequest{
		PersonID:   &personID,
		Amount:     "3500.00",
		IncomeDate: civildate.Of(time.Now()),
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Reviewed {
		t.Fatal("expected a newly created income entry to be unreviewed")
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Amount != "3500.00" {
		t.Fatalf("expected amount 3500.00, got %v", got.Amount)
	}

	list, err := service.List(ctx, nil, &personID, "")
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 income entry, got %d", len(list))
	}
}

func TestService_Update_MarksReviewed(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateEntryRequest{Amount: "500.00", IncomeDate: civildate.Of(time.Now())})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	updated, err := service.Update(ctx, created.ID, UpdateEntryRequest{
		Amount:     "500.00",
		IncomeDate: created.IncomeDate,
		Reviewed:   true,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if !updated.Reviewed {
		t.Fatal("expected the income entry to be marked reviewed after Update")
	}
}

func TestService_Get_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.Get(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.Update(ctx, uuid.New(), UpdateEntryRequest{Amount: "1.00", IncomeDate: civildate.Of(time.Now())})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	err := service.Delete(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Create_RejectsMismatchedUSDPair(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	usdAmount := "1000.00"

	_, err := service.Create(ctx, CreateEntryRequest{
		Amount: "5000.00", IncomeDate: civildate.Of(time.Now()), USDAmount: &usdAmount, // no ExchangeRate
	})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
		t.Fatalf("expected a validation apperr.Error for a lone usdAmount, got %v", err)
	}
}

func TestService_Create_PersistsUSDAmountAndExchangeRate(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	usdAmount, exchangeRate := "1000.00", "5.05"

	created, err := service.Create(ctx, CreateEntryRequest{
		Amount: "5050.00", IncomeDate: civildate.Of(time.Now()), USDAmount: &usdAmount, ExchangeRate: &exchangeRate,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.USDAmount == nil || *created.USDAmount != usdAmount {
		t.Fatalf("expected USDAmount %q to round-trip, got %v", usdAmount, created.USDAmount)
	}
	if created.ExchangeRate == nil || *created.ExchangeRate != exchangeRate {
		t.Fatalf("expected ExchangeRate %q to round-trip, got %v", exchangeRate, created.ExchangeRate)
	}
}

func TestService_MonthlySummary_ComputesEffectiveUSDRateAcrossMultipleEntries(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	month := civildate.Of(time.Date(2026, time.June, 10, 0, 0, 0, 0, time.UTC))

	// Two USD-paid entries in the same month at different rates — the effective rate must be
	// (total BRL ÷ total USD), not an average of 5.00 and 5.20 (which would give the wrong 5.10).
	usd1, rate1 := "1000.00", "5.00"
	if _, err := service.Create(ctx, CreateEntryRequest{Amount: "5000.00", IncomeDate: month, USDAmount: &usd1, ExchangeRate: &rate1}); err != nil {
		t.Fatalf("Create (first USD entry) returned error: %v", err)
	}
	usd2, rate2 := "1000.00", "5.20"
	if _, err := service.Create(ctx, CreateEntryRequest{Amount: "5200.00", IncomeDate: month, USDAmount: &usd2, ExchangeRate: &rate2}); err != nil {
		t.Fatalf("Create (second USD entry) returned error: %v", err)
	}
	// A plain BRL entry the same month must not pollute USDTotal/ConvertedBRLTotal.
	if _, err := service.Create(ctx, CreateEntryRequest{Amount: "1200.00", IncomeDate: month}); err != nil {
		t.Fatalf("Create (BRL-only entry) returned error: %v", err)
	}

	summary, err := service.MonthlySummary(ctx, 2026, 6)
	if err != nil {
		t.Fatalf("MonthlySummary returned error: %v", err)
	}
	if summary.USDTotal != "2000.00" {
		t.Fatalf("USDTotal = %q, want 2000.00", summary.USDTotal)
	}
	if summary.ConvertedBRLTotal != "10200.00" {
		t.Fatalf("ConvertedBRLTotal = %q, want 10200.00", summary.ConvertedBRLTotal)
	}
	if summary.AvgExchangeRate != "5.1000" {
		t.Fatalf("AvgExchangeRate = %q, want 5.1000 (10200/2000, not the arithmetic mean of the two rates)", summary.AvgExchangeRate)
	}
	// The gross total still includes every entry, USD-paid or not — USD tracking is informational.
	if summary.GrossEarnings != "11400.00" {
		t.Fatalf("GrossEarnings = %q, want 11400.00", summary.GrossEarnings)
	}
}

func TestService_MonthlySummary_OmitsUSDFieldsWhenNoEntryHasOne(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	month := civildate.Of(time.Date(2026, time.July, 5, 0, 0, 0, 0, time.UTC))

	if _, err := service.Create(ctx, CreateEntryRequest{Amount: "3000.00", IncomeDate: month}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	summary, err := service.MonthlySummary(ctx, 2026, 7)
	if err != nil {
		t.Fatalf("MonthlySummary returned error: %v", err)
	}
	if summary.USDTotal != "" || summary.ConvertedBRLTotal != "" || summary.AvgExchangeRate != "" {
		t.Fatalf("expected empty USD fields for a month with no USD-paid entries, got %+v", summary)
	}
}
