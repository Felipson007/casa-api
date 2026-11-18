package notifications

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	notifications map[uuid.UUID]Notification
	config        *Configuration
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{notifications: make(map[uuid.UUID]Notification)}
}

func (f *fakeRepository) List(_ context.Context, readOnly *bool) ([]Notification, error) {
	var result []Notification
	for _, n := range f.notifications {
		if readOnly != nil && n.Read != *readOnly {
			continue
		}
		result = append(result, n)
	}
	return result, nil
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Notification, error) {
	n, ok := f.notifications[id]
	if !ok {
		return nil, nil
	}
	return &n, nil
}

func (f *fakeRepository) FindEquivalent(_ context.Context, notificationType NotificationType, title, message string, link *string) (*Notification, error) {
	for _, n := range f.notifications {
		if n.Type == notificationType && n.Title == title && n.Message == message && linksEqual(n.Link, link) {
			return &n, nil
		}
	}
	return nil, nil
}

func linksEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (f *fakeRepository) Create(_ context.Context, n Notification) error {
	f.notifications[n.ID] = n
	return nil
}

func (f *fakeRepository) MarkRead(_ context.Context, id uuid.UUID) error {
	n, ok := f.notifications[id]
	if !ok {
		return errors.New("not found")
	}
	n.Read = true
	f.notifications[id] = n
	return nil
}

func (f *fakeRepository) MarkAllRead(_ context.Context) error {
	for id, n := range f.notifications {
		n.Read = true
		f.notifications[id] = n
	}
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.notifications, id)
	return nil
}

func (f *fakeRepository) DeleteAll(_ context.Context) error {
	clear(f.notifications)
	return nil
}

func (f *fakeRepository) GetConfiguration(_ context.Context) (*Configuration, error) {
	return f.config, nil
}

func (f *fakeRepository) CreateConfiguration(_ context.Context, c Configuration) error {
	f.config = &c
	return nil
}

func (f *fakeRepository) UpdateConfiguration(_ context.Context, c Configuration) error {
	if f.config == nil {
		return errors.New("not found")
	}
	f.config = &c
	return nil
}

func TestService_CreateGetListMarkRead(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateNotificationRequest{Type: NotificationTypeInstallmentDueSoon, Title: "Hi", Message: "Test"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Read {
		t.Fatal("expected a newly created notification to be unread")
	}

	unreadOnly := false
	list, err := service.List(ctx, &unreadOnly)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 unread notification, got %d", len(list))
	}

	if err := service.MarkRead(ctx, created.ID); err != nil {
		t.Fatalf("MarkRead returned error: %v", err)
	}
	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !got.Read {
		t.Fatal("expected notification to be read after MarkRead")
	}
}

func TestService_MarkAllRead(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	first, err := service.Create(ctx, CreateNotificationRequest{Type: NotificationTypeInstallmentDueSoon, Title: "A", Message: "Test"})
	if err != nil {
		t.Fatalf("Create (first) returned error: %v", err)
	}
	second, err := service.Create(ctx, CreateNotificationRequest{Type: NotificationTypeInstallmentDueSoon, Title: "B", Message: "Test"})
	if err != nil {
		t.Fatalf("Create (second) returned error: %v", err)
	}
	if err := service.MarkRead(ctx, first.ID); err != nil {
		t.Fatalf("MarkRead returned error: %v", err)
	}

	if err := service.MarkAllRead(ctx); err != nil {
		t.Fatalf("MarkAllRead returned error: %v", err)
	}

	unreadOnly := false
	list, err := service.List(ctx, &unreadOnly)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected zero unread notifications after MarkAllRead, got %d", len(list))
	}
	gotSecond, err := service.Get(ctx, second.ID)
	if err != nil {
		t.Fatalf("Get (second) returned error: %v", err)
	}
	if !gotSecond.Read {
		t.Fatal("expected the previously-unread second notification to be marked read too")
	}
}

func TestService_CreateRecurringEventIsIdempotent(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	link := "/emprestimos/detalhe?id=" + uuid.NewString()
	req := CreateNotificationRequest{
		Type: NotificationTypeInstallmentDueSoon, Title: "Installment due soon",
		Message: "Installment #2 is due 2026-08-25.", Link: &link,
	}

	first, err := service.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	second, err := service.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected the existing notification ID %s, got %s", first.ID, second.ID)
	}
	if len(repo.notifications) != 1 {
		t.Fatalf("expected one persisted notification, got %d", len(repo.notifications))
	}
}

func TestService_DeleteAll(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	for _, title := range []string{"First", "Second"} {
		if _, err := service.Create(ctx, CreateNotificationRequest{
			Type: NotificationTypeBackupCompleted, Title: title, Message: "done",
		}); err != nil {
			t.Fatalf("Create %s: %v", title, err)
		}
	}

	if err := service.DeleteAll(ctx); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	if len(repo.notifications) != 0 {
		t.Fatalf("expected no notifications, got %d", len(repo.notifications))
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

func TestService_MarkRead_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	err := service.MarkRead(ctx, uuid.New())

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

func TestService_GetConfiguration_LazyProvisions(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	config, err := service.GetConfiguration(ctx)
	if err != nil {
		t.Fatalf("GetConfiguration returned error: %v", err)
	}
	if config.RetentionDays != defaultRetentionDays {
		t.Fatalf("expected default retention days %d, got %d", defaultRetentionDays, config.RetentionDays)
	}

	// A second call must return the SAME provisioned row, not create another one.
	again, err := service.GetConfiguration(ctx)
	if err != nil {
		t.Fatalf("second GetConfiguration returned error: %v", err)
	}
	if again.ID != config.ID {
		t.Fatalf("expected the same configuration ID on repeat reads, got %v then %v", config.ID, again.ID)
	}
}

func TestService_UpdateConfiguration(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	updated, err := service.UpdateConfiguration(ctx, UpdateConfigurationRequest{RetentionDays: 30})
	if err != nil {
		t.Fatalf("UpdateConfiguration returned error: %v", err)
	}
	if updated.RetentionDays != 30 {
		t.Fatalf("expected retention days 30, got %d", updated.RetentionDays)
	}
}
