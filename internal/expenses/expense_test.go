package expenses

import (
	"context"
	"errors"
	"sort"
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
	byID          map[uuid.UUID]Expense
	categoryNames map[uuid.UUID]string
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byID: make(map[uuid.UUID]Expense), categoryNames: make(map[uuid.UUID]string)}
}

func (f *fakeRepository) categoryNameFor(e Expense) string {
	if e.CategoryID == nil {
		return "Sem categoria"
	}
	if name, ok := f.categoryNames[*e.CategoryID]; ok {
		return name
	}
	return "Sem categoria"
}

func (f *fakeRepository) ListInRange(_ context.Context, from, toExclusive time.Time) ([]ExpenseWithCategoryName, error) {
	var result []ExpenseWithCategoryName
	for _, e := range f.byID {
		if !e.ExpenseDate.Before(from) && e.ExpenseDate.Before(toExclusive) {
			result = append(result, ExpenseWithCategoryName{Expense: e, CategoryName: f.categoryNameFor(e)})
		}
	}
	return result, nil
}

func (f *fakeRepository) ListRecurringSince(_ context.Context, since time.Time) ([]ExpenseWithCategoryName, error) {
	var result []ExpenseWithCategoryName
	for _, e := range f.byID {
		if e.Recurring && !e.ExpenseDate.Before(since) {
			result = append(result, ExpenseWithCategoryName{Expense: e, CategoryName: f.categoryNameFor(e)})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ExpenseDate.After(result[j].ExpenseDate) })
	return result, nil
}

func (f *fakeRepository) List(_ context.Context, categoryID, personID *uuid.UUID, query string) ([]Expense, error) {
	var result []Expense
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

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Expense, error) {
	e, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (f *fakeRepository) Create(_ context.Context, e Expense) error {
	f.byID[e.ID] = e
	return nil
}

func (f *fakeRepository) Update(_ context.Context, e Expense) error {
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

func (f *fakeRepository) ListByDocument(_ context.Context, documentID uuid.UUID) ([]Expense, error) {
	var result []Expense
	for _, e := range f.byID {
		if e.DocumentID != nil && *e.DocumentID == documentID {
			result = append(result, e)
		}
	}
	return result, nil
}

func (f *fakeRepository) ReplaceForDocument(_ context.Context, documentID uuid.UUID, es []Expense) error {
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

func TestService_CreateGetList(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	categoryID := uuid.New()

	created, err := service.Create(ctx, CreateExpenseRequest{
		CategoryID:  &categoryID,
		Amount:      "45.90",
		ExpenseDate: civildate.Of(time.Now()),
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Reviewed {
		t.Fatal("expected a newly created expense to be unreviewed")
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Amount != "45.90" {
		t.Fatalf("expected amount 45.90, got %v", got.Amount)
	}

	list, err := service.List(ctx, &categoryID, nil, "")
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 expense, got %d", len(list))
	}
}

func TestService_Update_MarksReviewed(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateExpenseRequest{Amount: "10.00", ExpenseDate: civildate.Of(time.Now())})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	updated, err := service.Update(ctx, created.ID, UpdateExpenseRequest{
		Amount:      "10.00",
		ExpenseDate: created.ExpenseDate,
		Reviewed:    true,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if !updated.Reviewed {
		t.Fatal("expected the expense to be marked reviewed after Update")
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

	_, err := service.Update(ctx, uuid.New(), UpdateExpenseRequest{Amount: "1.00", ExpenseDate: civildate.Of(time.Now())})

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

func TestService_MonthlySummary_ByDayProjectsOriginalPurchaseDayNotCompetencia(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	// Competência (ExpenseDate) is the 1st, like every real invoice line — OriginalDate (the real
	// purchase date) is the 15th. ByDay must bucket by day 15 of the queried month, not day 1.
	original := time.Date(2026, time.February, 15, 0, 0, 0, 0, time.UTC)
	e := Expense{
		ID: uuid.New(), Amount: "100.00", ExpenseDate: time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC),
		OriginalDate: &original, Reviewed: false,
	}
	repo.byID[e.ID] = e

	summary, err := service.MonthlySummary(ctx, 2026, 3)
	if err != nil {
		t.Fatalf("MonthlySummary returned error: %v", err)
	}
	if len(summary.ByDay) != 1 {
		t.Fatalf("expected exactly 1 day bucket, got %d", len(summary.ByDay))
	}
	if got := summary.ByDay[0].Day; got.Day() != 15 || got.Month() != time.March {
		t.Fatalf("expected day 15 of March (projected from OriginalDate), got %v", got)
	}
}

func TestService_MonthlySummary_ByDayClampsToMonthLength(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	// OriginalDate's day-of-month (31) doesn't exist in February — must clamp to the 28th (2026 is
	// not a leap year).
	original := time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC)
	e := Expense{
		ID: uuid.New(), Amount: "50.00", ExpenseDate: time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
		OriginalDate: &original,
	}
	repo.byID[e.ID] = e

	summary, err := service.MonthlySummary(ctx, 2026, 2)
	if err != nil {
		t.Fatalf("MonthlySummary returned error: %v", err)
	}
	if len(summary.ByDay) != 1 || summary.ByDay[0].Day.Day() != 28 {
		t.Fatalf("expected day clamped to 28 (Feb 2026's length), got %+v", summary.ByDay)
	}
}

func TestService_Subscriptions_DetectsPriceIncrease(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	now := time.Date(2026, time.June, 15, 0, 0, 0, 0, time.UTC)
	desc := "Netflix"

	// Five months at 20.00, then a 6th at 22.90 — a ~14% jump: over the 10% price-increase
	// threshold, but within the same-plan amount band (a 50% swing would be a different charge).
	for i, amount := range []string{"20.00", "20.00", "20.00", "20.00", "20.00", "22.90"} {
		d := desc
		date := now.AddDate(0, -(5 - i), 0)
		e := Expense{ID: uuid.New(), Amount: amount, ExpenseDate: date, Description: &d, Recurring: true}
		repo.byID[e.ID] = e
	}

	subs, err := service.Subscriptions(ctx, now)
	if err != nil {
		t.Fatalf("Subscriptions returned error: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription group, got %d: %+v", len(subs), subs)
	}
	if !subs[0].PriceIncreaseDetected {
		t.Fatalf("expected a price increase to be detected, got %+v", subs[0])
	}
	if subs[0].CurrentAmount != "22.90" {
		t.Fatalf("expected current amount 22.90 (most recent), got %s", subs[0].CurrentAmount)
	}
}

func TestNextChargeOnOrAfter(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

	// Jan 31 + 1 month, clamped to Feb's last day.
	if got := nextChargeOnOrAfter(d(2026, time.January, 31), d(2026, time.February, 1)); !got.Equal(d(2026, time.February, 28)) {
		t.Fatalf("clamp: expected 2026-02-28, got %s", got)
	}
	// Newest charge weeks old: roll forward past ref, never into the past.
	if got := nextChargeOnOrAfter(d(2026, time.January, 10), d(2026, time.April, 20)); !got.Equal(d(2026, time.May, 10)) {
		t.Fatalf("rollforward: expected 2026-05-10, got %s", got)
	}
	// ref falls before this month's anniversary: the anniversary this month is the answer.
	if got := nextChargeOnOrAfter(d(2026, time.January, 15), d(2026, time.March, 3)); !got.Equal(d(2026, time.March, 15)) {
		t.Fatalf("same-month: expected 2026-03-15, got %s", got)
	}
}

func TestService_Subscriptions_IgnoresEntriesWithNoDescription(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	now := time.Now()

	repo.byID[uuid.New()] = Expense{ID: uuid.New(), Amount: "10.00", ExpenseDate: now, Recurring: true, Description: nil}

	subs, err := service.Subscriptions(ctx, now)
	if err != nil {
		t.Fatalf("Subscriptions returned error: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("expected no subscription groups for a description-less entry, got %+v", subs)
	}
}

func TestService_Autocategorize_InheritsFromManualHistory(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	transport := uuid.New()
	repo.categoryNames[transport] = "Transporte"
	uber := "Uber"
	known, err := service.Create(ctx, CreateExpenseRequest{
		CategoryID: &transport, Description: &uber, Amount: "20.00", ExpenseDate: civildate.Of(time.Now()),
	})
	if err != nil {
		t.Fatalf("Create known returned error: %v", err)
	}
	if known.CategorySource != CategorySourceManual {
		t.Fatalf("a hand-created row with a category should be MANUAL, got %q", known.CategorySource)
	}

	trip := "UBER *TRIP"
	pending, err := service.Create(ctx, CreateExpenseRequest{
		Description: &trip, Amount: "30.00", ExpenseDate: civildate.Of(time.Now()),
	})
	if err != nil {
		t.Fatalf("Create pending returned error: %v", err)
	}
	if pending.CategorySource != CategorySourceNone {
		t.Fatalf("an uncategorised row should be NONE, got %q", pending.CategorySource)
	}

	updated, err := service.Autocategorize(ctx)
	if err != nil {
		t.Fatalf("Autocategorize returned error: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected 1 row filled, got %d", updated)
	}

	filled, err := service.Get(ctx, pending.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if filled.CategoryID == nil || *filled.CategoryID != transport {
		t.Fatalf("expected the pending row to inherit Transporte, got %+v", filled.CategoryID)
	}
	if filled.CategorySource != CategorySourceHistory {
		t.Fatalf("expected HISTORY provenance, got %q", filled.CategorySource)
	}

	kept, err := service.Get(ctx, known.ID)
	if err != nil {
		t.Fatalf("Get known returned error: %v", err)
	}
	if kept.CategorySource != CategorySourceManual {
		t.Fatalf("the manual row must keep its MANUAL provenance, got %q", kept.CategorySource)
	}
}

func TestService_Update_ChangingCategoryMarksManual(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateExpenseRequest{Amount: "10.00", ExpenseDate: civildate.Of(time.Now())})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.CategorySource != CategorySourceNone {
		t.Fatalf("expected NONE, got %q", created.CategorySource)
	}

	categoryID := uuid.New()
	updated, err := service.Update(ctx, created.ID, UpdateExpenseRequest{
		CategoryID: &categoryID, Amount: "10.00", ExpenseDate: created.ExpenseDate,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.CategorySource != CategorySourceManual {
		t.Fatalf("changing the category should mark MANUAL, got %q", updated.CategorySource)
	}
}
