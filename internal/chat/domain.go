// Package chat is the "IA Atlas" chat-message persistence module (chat_mensagens/ia in the old
// app) — owned by Claude per decisions/DECISIONS.md D-005.
//
// Scope note: this module persists messages only — List/Create/DeleteAll, scoped to the
// authenticated app user (each person has their own conversation, not a shared household
// resource like every other module). It deliberately does NOT implement the actual AI
// orchestration the old Java ChatService did: choosing a model provider, running the
// tool-calling loop, SSE streaming, or provider fallback (br.com.atlas.ia.service.ChatService,
// ChatModelRouter, ChatToolExecutor). That logic needs real LLM provider credentials and crosses
// into the ai/integrations modules — out of scope until those exist. Posting a Role: RoleUser
// message today just logs it; nothing generates a RoleAssistant reply automatically yet. A future
// AI-orchestration layer is the intended caller of Create for RoleAssistant messages.
package chat

import (
	"time"

	"github.com/google/uuid"
)

// Role mirrors the chat_messages.role CHECK constraint — unlike most enums in this codebase, this
// one already has a DB CHECK. Unlike the Java ChatPapel, which only has USUARIO/ASSISTENTE, the
// schema also allows SYSTEM for a future system-prompt message.
type Role string

const (
	RoleUser      Role = "USER"
	RoleAssistant Role = "ASSISTANT"
	RoleSystem    Role = "SYSTEM"
)

// ToolCall is one entry of Message.ToolCalls — mirrors the old Java
// ChatFerramentaChamadaDto{nome, argumentos}, kept purely for UI transparency ("which tool did the
// assistant call to get this answer"), never executed by this module.
type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// Message mirrors the chat_messages table (internal/migrations/000001_init_schema.up.sql).
// ModelUsed is nil for user messages (only ever set for assistant replies). ToolCalls is nullable
// jsonb — nil means no tool was called for that message.
type Message struct {
	ID        uuid.UUID
	AppUserID uuid.UUID
	Role      Role
	Content   string
	ModelUsed *string
	ToolCalls []ToolCall
	CreatedAt time.Time
}
