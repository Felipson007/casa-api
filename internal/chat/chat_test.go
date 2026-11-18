package chat

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	byUser map[uuid.UUID][]Message
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byUser: make(map[uuid.UUID][]Message)}
}

func (f *fakeRepository) List(_ context.Context, appUserID uuid.UUID) ([]Message, error) {
	return f.byUser[appUserID], nil
}

func (f *fakeRepository) Create(_ context.Context, m Message) error {
	f.byUser[m.AppUserID] = append(f.byUser[m.AppUserID], m)
	return nil
}

func (f *fakeRepository) DeleteAll(_ context.Context, appUserID uuid.UUID) error {
	delete(f.byUser, appUserID)
	return nil
}

func TestService_CreateListDeleteAll(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	appUserID := uuid.New()
	otherUserID := uuid.New()

	created, err := service.Create(ctx, appUserID, CreateMessageRequest{Role: RoleUser, Content: "Quanto devo no total?"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Role != RoleUser {
		t.Fatalf("expected role USER, got %v", created.Role)
	}

	toolCalls := []ToolCall{{Name: "loans_summary", Arguments: map[string]any{"personId": "abc"}}}
	modelUsed := "gemini-3.1-flash-lite"
	if _, err := service.Create(ctx, appUserID, CreateMessageRequest{
		Role:      RoleAssistant,
		Content:   "Voce deve R$500.",
		ModelUsed: &modelUsed,
		ToolCalls: toolCalls,
	}); err != nil {
		t.Fatalf("Create (assistant) returned error: %v", err)
	}

	// A different app user's history must never leak into this one's list.
	if _, err := service.Create(ctx, otherUserID, CreateMessageRequest{Role: RoleUser, Content: "oi"}); err != nil {
		t.Fatalf("Create (other user) returned error: %v", err)
	}

	list, err := service.List(ctx, appUserID)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 messages for appUserID, got %d", len(list))
	}
	if list[1].ModelUsed == nil || *list[1].ModelUsed != "gemini-3.1-flash-lite" {
		t.Fatalf("expected second message's modelUsed to round-trip, got %v", list[1].ModelUsed)
	}
	if len(list[1].ToolCalls) != 1 || list[1].ToolCalls[0].Name != "loans_summary" {
		t.Fatalf("expected ToolCalls to round-trip, got %+v", list[1].ToolCalls)
	}

	if err := service.DeleteAll(ctx, appUserID); err != nil {
		t.Fatalf("DeleteAll returned error: %v", err)
	}
	list, err = service.List(ctx, appUserID)
	if err != nil {
		t.Fatalf("List after DeleteAll returned error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty history after DeleteAll, got %d messages", len(list))
	}

	// The other user's history must survive appUserID's DeleteAll.
	otherList, err := service.List(ctx, otherUserID)
	if err != nil {
		t.Fatalf("List (other user) returned error: %v", err)
	}
	if len(otherList) != 1 {
		t.Fatalf("expected other user's history untouched, got %d messages", len(otherList))
	}
}
