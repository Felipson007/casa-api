package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	List(ctx context.Context, appUserID uuid.UUID) ([]Message, error)
	Create(ctx context.Context, m Message) error
	DeleteAll(ctx context.Context, appUserID uuid.UUID) error
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13).
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns the given app user's conversation, oldest first — mirrors the old Java
// ChatService.historico ordering.
func (s *Service) List(ctx context.Context, appUserID uuid.UUID) ([]MessageDTO, error) {
	list, err := s.repo.List(ctx, appUserID)
	if err != nil {
		return nil, fmt.Errorf("chat: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Create persists one message for the given app user. See the package doc: this does not
// generate a reply — a future AI-orchestration layer is the intended caller for RoleAssistant
// messages.
func (s *Service) Create(ctx context.Context, appUserID uuid.UUID, req CreateMessageRequest) (MessageDTO, error) {
	// CreatedAt is set explicitly here (app clock), not left to the chat_messages.created_at DB
	// default — this module has no FindByID/Get to re-read the persisted row after Create (no
	// /{id} routes exist, by design), unlike every other module's Create, which re-reads instead
	// (found systemically by Hermes's HTTP endpoint sweep on loans, msg 83 F-3). Truncated to
	// microsecond precision to match what Postgres's timestamptz actually stores — Go's clock is
	// nanosecond-precision, so without this the 201 response's createdAt (nanoseconds) wouldn't
	// byte-for-byte match a later read of the same row (microseconds) — F-3b, msg 90.
	m := Message{
		ID:        uuid.New(),
		AppUserID: appUserID,
		Role:      req.Role,
		Content:   req.Content,
		ModelUsed: req.ModelUsed,
		ToolCalls: req.ToolCalls,
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	if err := s.repo.Create(ctx, m); err != nil {
		return MessageDTO{}, err // already an *apperr.Error (FK violation) or a wrapped DB error
	}
	return toDTO(m), nil
}

// DeleteAll clears the given app user's entire conversation — mirrors the old Java
// ChatService.limparHistorico.
func (s *Service) DeleteAll(ctx context.Context, appUserID uuid.UUID) error {
	if err := s.repo.DeleteAll(ctx, appUserID); err != nil {
		return fmt.Errorf("chat: clearing history: %w", err)
	}
	return nil
}
