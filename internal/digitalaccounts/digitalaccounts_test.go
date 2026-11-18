package digitalaccounts

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	byID map[uuid.UUID]Account
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byID: make(map[uuid.UUID]Account)}
}

func (f *fakeRepository) List(_ context.Context, personID *uuid.UUID, category *Category, status *Status, email *string) ([]Account, error) {
	var result []Account
	for _, a := range f.byID {
		if personID != nil && (a.PersonID == nil || *a.PersonID != *personID) {
			continue
		}
		if category != nil && a.Category != *category {
			continue
		}
		if status != nil && a.Status != *status {
			continue
		}
		if email != nil && (a.EmailUsed == nil || *a.EmailUsed != *email) {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Account, error) {
	a, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &a, nil
}

func (f *fakeRepository) Create(_ context.Context, a Account) error {
	f.byID[a.ID] = a
	return nil
}

func (f *fakeRepository) Update(_ context.Context, a Account) error {
	if _, ok := f.byID[a.ID]; !ok {
		return errors.New("not found")
	}
	f.byID[a.ID] = a
	return nil
}

func (f *fakeRepository) MarkReviewed(_ context.Context, id uuid.UUID) error {
	a, ok := f.byID[id]
	if !ok {
		return errors.New("not found")
	}
	f.byID[id] = a
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.byID, id)
	return nil
}

func ptr(s string) *string { return &s }

func TestService_CreateGetList(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateAccountRequest{ServiceName: "GitHub", Category: CategoryDevelopment})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Status != StatusActive {
		t.Fatalf("expected a newly created account to be ACTIVE, got %v", created.Status)
	}
	if created.MFAMethod != MFANone {
		t.Fatalf("expected omitted MFA to default to NONE, got %v", created.MFAMethod)
	}

	list, err := service.List(ctx, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 account, got %d", len(list))
	}
}

func TestService_Get_NotFound(t *testing.T) {
	service := NewService(newFakeRepository())

	_, err := service.Get(context.Background(), uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Import_SkipsDuplicates(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	if _, err := service.Create(ctx, CreateAccountRequest{
		ServiceName: "GitHub", Category: CategoryDevelopment, EmailUsed: ptr("me@example.com"),
	}); err != nil {
		t.Fatalf("seeding Create returned error: %v", err)
	}

	result, err := service.Import(ctx, ImportAccountsRequest{Items: []ImportAccountItem{
		{ServiceName: "GitHub", Category: CategoryDevelopment, EmailUsed: ptr("me@example.com")},
		{ServiceName: " github ", Category: CategoryDevelopment, EmailUsed: ptr("ME@EXAMPLE.COM")},
		{ServiceName: "GitLab", Category: CategoryDevelopment, EmailUsed: ptr("me@example.com")},
		{ServiceName: "GitHub", Category: CategoryDevelopment},
	}})
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}

	if result.Created != 2 || result.Skipped != 2 {
		t.Fatalf("expected created=2 skipped=2, got created=%d skipped=%d", result.Created, result.Skipped)
	}
	if len(result.Items) != 4 {
		t.Fatalf("expected one result per item, got %d", len(result.Items))
	}
	if result.Items[0].Outcome != ImportOutcomeSkipped || result.Items[1].Outcome != ImportOutcomeSkipped {
		t.Fatalf("expected the two GitHub/me@example.com variants to be skipped, got %v/%v", result.Items[0].Outcome, result.Items[1].Outcome)
	}
	if result.Items[2].Outcome != ImportOutcomeCreated || result.Items[3].Outcome != ImportOutcomeCreated {
		t.Fatalf("expected GitLab-by-email and GitHub-without-email to be created, got %v/%v", result.Items[2].Outcome, result.Items[3].Outcome)
	}

	// Trimmed on the way in, not stored with the export file's stray spaces.
	for _, a := range repo.byID {
		if a.ServiceName != "GitHub" && a.ServiceName != "GitLab" {
			t.Fatalf("unexpected service name stored: %q", a.ServiceName)
		}
	}
}

func TestService_Import_DedupesWithinRequest(t *testing.T) {
	service := NewService(newFakeRepository())

	result, err := service.Import(context.Background(), ImportAccountsRequest{Items: []ImportAccountItem{
		{ServiceName: "Netflix", Category: CategoryStreaming, EmailUsed: ptr("me@example.com")},
		{ServiceName: "Netflix", Category: CategoryStreaming, EmailUsed: ptr("me@example.com")},
	}})
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}
	if result.Created != 1 || result.Skipped != 1 {
		t.Fatalf("expected created=1 skipped=1 within one request, got created=%d skipped=%d", result.Created, result.Skipped)
	}
}

func TestService_Import_DefaultsMFAAndStatus(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)

	if _, err := service.Import(context.Background(), ImportAccountsRequest{Items: []ImportAccountItem{
		{ServiceName: "Nubank", Category: CategoryBanking},
	}}); err != nil {
		t.Fatalf("Import returned error: %v", err)
	}

	for _, a := range repo.byID {
		if a.MFAMethod != MFANone {
			t.Fatalf("expected default MFA NONE, got %v", a.MFAMethod)
		}
		if a.Status != StatusActive {
			t.Fatalf("expected imported accounts to be ACTIVE, got %v", a.Status)
		}
	}
}
