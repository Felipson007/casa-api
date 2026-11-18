//go:build integration

package chat_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/chat"
	"github.com/atlas/atlas-go/internal/chat/postgres"
)

// TestRepository_CreateListDeleteAll requires a real app_users row first (chat_messages.app_user_id
// is NOT NULL with a foreign key) — this test creates and cleans up its own user row directly.
// Gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/chat/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestRepository_CreateListDeleteAll(t *testing.T) {
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

	appUserID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO app_users (id, email, role) VALUES ($1, $2, 'MEMBER')`,
		appUserID, appUserID.String()+"@test.local"); err != nil {
		t.Fatalf("seeding test app_user: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM app_users WHERE id = $1`, appUserID) //nolint:errcheck

	repo := postgres.NewRepository(pool)
	service := chat.NewService(repo)

	if _, err := service.Create(ctx, appUserID, chat.CreateMessageRequest{
		Role: chat.RoleUser, Content: "Quanto devo no total?",
	}); err != nil {
		t.Fatalf("Create (user message): %v", err)
	}

	modelUsed := "gemini-3.1-flash-lite"
	created, err := service.Create(ctx, appUserID, chat.CreateMessageRequest{
		Role:      chat.RoleAssistant,
		Content:   "Voce deve R$500.",
		ModelUsed: &modelUsed,
		ToolCalls: []chat.ToolCall{{Name: "loans_summary", Arguments: map[string]any{"personId": "abc"}}},
	})
	if err != nil {
		t.Fatalf("Create (assistant message): %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM chat_messages WHERE app_user_id = $1`, appUserID) //nolint:errcheck

	if created.ModelUsed == nil || *created.ModelUsed != "gemini-3.1-flash-lite" {
		t.Fatalf("expected modelUsed to round-trip, got %v", created.ModelUsed)
	}
	if len(created.ToolCalls) != 1 || created.ToolCalls[0].Name != "loans_summary" {
		t.Fatalf("expected ToolCalls to round-trip through jsonb, got %+v", created.ToolCalls)
	}

	list, err := service.List(ctx, appUserID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(list))
	}
	if list[0].Role != chat.RoleUser || list[1].Role != chat.RoleAssistant {
		t.Fatalf("expected chronological order USER then ASSISTANT, got %v then %v", list[0].Role, list[1].Role)
	}

	if err := service.DeleteAll(ctx, appUserID); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	list, err = service.List(ctx, appUserID)
	if err != nil {
		t.Fatalf("List after DeleteAll: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty history after DeleteAll, got %d messages", len(list))
	}
}
