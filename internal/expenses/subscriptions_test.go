package expenses

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// now is mid-August 2026 for every case here, so a charge in "2026-08" is current.
var subsNow = time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)

func charge(t *testing.T, amount, month string, day int) ExpenseWithCategoryName {
	t.Helper()
	m, err := time.Parse("2006-01", month)
	if err != nil {
		t.Fatalf("bad test month %q: %v", month, err)
	}
	desc := "x"
	return ExpenseWithCategoryName{
		Expense: Expense{
			Amount:      amount,
			ExpenseDate: time.Date(m.Year(), m.Month(), day, 0, 0, 0, 0, time.UTC),
			Description: &desc,
		},
	}
}

// TestLooksLikeSubscription_RejectsFrequentMerchants uses the three real entries that were wrongly
// listed as subscriptions in production: Uber (101 charges in one month), "iFood - Rodrigo"
// (2 charges) and Nuuvem (2 charges). None is a subscription; each fails a different criterion.
func TestLooksLikeSubscription_RejectsFrequentMerchants(t *testing.T) {
	// Uber: plenty of months, but many charges per month and wildly varying amounts.
	var uber []ExpenseWithCategoryName
	for _, month := range []string{"2026-06", "2026-07", "2026-08"} {
		for day := 1; day <= 30; day++ {
			uber = append(uber, charge(t, "24.47", month, day))
		}
	}
	if looksLikeSubscription(uber, subsNow) {
		t.Error("a merchant charging ~30 times a month is not a subscription")
	}

	// iFood / Nuuvem: only two charges, so not enough months to establish a pattern.
	twoCharges := []ExpenseWithCategoryName{
		charge(t, "27.58", "2026-07", 31),
		charge(t, "27.58", "2026-08", 12),
	}
	if looksLikeSubscription(twoCharges, subsNow) {
		t.Error("two charges are a coincidence, not a subscription")
	}

	// Three months, once a month, but the amount swings far too much to be a subscription.
	variable := []ExpenseWithCategoryName{
		charge(t, "12.00", "2026-06", 3),
		charge(t, "31.93", "2026-07", 14),
		charge(t, "88.40", "2026-08", 22),
	}
	if looksLikeSubscription(variable, subsNow) {
		t.Error("wildly varying amounts are not a subscription")
	}
}

// TestLooksLikeSubscription_RejectsLapsedAndSporadic covers the "iFood - Gabriel" report: three
// same-ish charges in Feb/Mar/Apr, flagged in August with a next-charge date months in the past.
func TestLooksLikeSubscription_RejectsLapsedAndSporadic(t *testing.T) {
	// Lapsed: same amount, consecutive months, but it stopped billing 4 months ago.
	lapsed := []ExpenseWithCategoryName{
		charge(t, "34.90", "2026-04", 1),
		charge(t, "34.90", "2026-03", 1),
		charge(t, "34.90", "2026-02", 1),
	}
	if looksLikeSubscription(lapsed, subsNow) {
		t.Error("a plan whose newest charge is 4 months old has lapsed — not a current subscription")
	}

	// Sporadic: recent and same amount, but the months have gaps (Feb, Apr, Aug).
	sporadic := []ExpenseWithCategoryName{
		charge(t, "15.00", "2026-08", 5),
		charge(t, "15.00", "2026-04", 5),
		charge(t, "15.00", "2026-02", 5),
	}
	if looksLikeSubscription(sporadic, subsNow) {
		t.Error("charges in non-consecutive months are a sporadic merchant, not a monthly plan")
	}
}

func TestLooksLikeSubscription_AcceptsRealOnes(t *testing.T) {
	steady := []ExpenseWithCategoryName{
		charge(t, "39.90", "2026-08", 15),
		charge(t, "39.90", "2026-07", 15),
		charge(t, "39.90", "2026-06", 15),
	}
	if !looksLikeSubscription(steady, subsNow) {
		t.Error("same amount, once a month, three months running IS a subscription")
	}

	// A modest price increase must not disqualify it — reporting that increase is the point of
	// PriceIncreaseDetected.
	raised := []ExpenseWithCategoryName{
		charge(t, "44.90", "2026-08", 15),
		charge(t, "39.90", "2026-07", 15),
		charge(t, "39.90", "2026-06", 15),
	}
	if !looksLikeSubscription(raised, subsNow) {
		t.Error("a subscription that got ~12% more expensive is still a subscription")
	}

	// One extra charge in a month (a proration, an annual plan beside a monthly one) is tolerated.
	withProration := []ExpenseWithCategoryName{
		charge(t, "39.90", "2026-08", 16),
		charge(t, "39.90", "2026-08", 15),
		charge(t, "39.90", "2026-07", 15),
		charge(t, "39.90", "2026-06", 15),
	}
	if !looksLikeSubscription(withProration, subsNow) {
		t.Error("a single extra charge should not disqualify a subscription")
	}
}

// TestService_Subscriptions_ManualRecurringSkipsHeuristic covers the fix for a real report: the
// user marks a manually entered expense (no DocumentID) as Recurring in the edit form, and expects
// it to show up as a subscription right away — not after 3 months of history. Since
// curator.markRepeatedInvoiceExpensesRecurring never touches DocumentID == nil rows, Recurring on
// a manual expense can only be the user's own statement, so looksLikeSubscription shouldn't apply.
func TestService_Subscriptions_ManualRecurringSkipsHeuristic(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	desc := "Academia"

	repo.byID[uuid.New()] = Expense{
		ID:          uuid.New(),
		DocumentID:  nil, // manually entered — user's own Recurring statement
		Amount:      "150.00",
		ExpenseDate: time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC),
		Description: &desc,
		Recurring:   true,
	}

	subs, err := service.Subscriptions(ctx, subsNow)
	if err != nil {
		t.Fatalf("Subscriptions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected the single manual recurring expense to appear immediately, got %d subscriptions", len(subs))
	}
	if subs[0].Name != desc {
		t.Errorf("expected subscription named %q, got %q", desc, subs[0].Name)
	}
	if subs[0].Occurrences != 1 {
		t.Errorf("expected 1 occurrence, got %d", subs[0].Occurrences)
	}
}

// TestService_Subscriptions_ImportedInvoiceStillNeedsHeuristic covers the other half: an invoice
// line (DocumentID set) with Recurring == true — set by curator's own heuristic, not the user
// directly — must still pass looksLikeSubscription. A single occurrence must not show up yet.
func TestService_Subscriptions_ImportedInvoiceStillNeedsHeuristic(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	desc := "iFood - Rodrigo"
	documentID := uuid.New()

	repo.byID[uuid.New()] = Expense{
		ID:          uuid.New(),
		DocumentID:  &documentID, // imported from an invoice — Recurring came from the heuristic
		Amount:      "27.58",
		ExpenseDate: time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC),
		Description: &desc,
		Recurring:   true,
	}

	subs, err := service.Subscriptions(ctx, subsNow)
	if err != nil {
		t.Fatalf("Subscriptions: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("expected a single imported charge to NOT qualify as a subscription yet, got %d", len(subs))
	}
}

// TestService_Subscriptions_ManualRecurringWithMultipleOccurrences guards the "most recent decides"
// rule when the same description has both a manual and an imported occurrence: the newest one
// being manual is what makes it trust the flag immediately, regardless of the older rows' source.
func TestService_Subscriptions_ManualRecurringWithMultipleOccurrences(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	desc := "Streaming X"
	documentID := uuid.New()

	repo.byID[uuid.New()] = Expense{
		ID:          uuid.New(),
		DocumentID:  &documentID,
		Amount:      "29.90",
		ExpenseDate: time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC),
		Description: &desc,
		Recurring:   true,
	}
	repo.byID[uuid.New()] = Expense{
		ID:          uuid.New(),
		DocumentID:  nil, // the user re-entered/confirmed it manually this month
		Amount:      "29.90",
		ExpenseDate: time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC),
		Description: &desc,
		Recurring:   true,
	}

	subs, err := service.Subscriptions(ctx, subsNow)
	if err != nil {
		t.Fatalf("Subscriptions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected the group to qualify because its most recent charge is manual, got %d", len(subs))
	}
}
