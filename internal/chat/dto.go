package chat

import (
	"time"

	"github.com/google/uuid"
)

// MessageDTO is the JSON shape returned to clients.
type MessageDTO struct {
	ID        uuid.UUID  `json:"id"`
	Role      Role       `json:"role"`
	Content   string     `json:"content"`
	ModelUsed *string    `json:"modelUsed,omitempty"`
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// CreateMessageRequest is the JSON body accepted by POST /api/chat/messages. AppUserID is not
// part of the request body — it's resolved from the authenticated caller (see Handler.Create).
type CreateMessageRequest struct {
	Role      Role       `json:"role" validate:"required,oneof=USER ASSISTANT SYSTEM"`
	Content   string     `json:"content" validate:"required"`
	ModelUsed *string    `json:"modelUsed"`
	ToolCalls []ToolCall `json:"toolCalls"`
}
