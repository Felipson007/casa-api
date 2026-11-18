package assistant

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/chat"
	"github.com/atlas/atlas-go/internal/pandora"
)

// pendingActionTTL bounds how long a write-tool confirmation stays valid — a stale card the user
// never clicked (tab left open overnight, etc.) shouldn't execute on a late click. No background
// sweep: expiry is checked lazily on take(), which is enough for a single-user, low-traffic app.
const pendingActionTTL = 15 * time.Minute

// pendingAction is the paused state of one turn, captured the moment the model requests a write
// tool: everything runLoop needs to resume — the conversation-so-far (including any read-tool
// results already resolved this turn) and the specific call awaiting approval.
type pendingAction struct {
	AppUserID   uuid.UUID
	Messages    []pandora.ChatCompletionMessage
	CalledTools []chat.ToolCall
	ToolCall    pandora.ToolCallOut
	CreatedAt   time.Time
}

// PendingConfirmation is what the handler exposes to the frontend when a turn pauses — enough to
// render a confirm/cancel card without leaking the internal conversation state.
type PendingConfirmation struct {
	ID        string
	ToolName  string
	Arguments map[string]any
}

// pendingStore holds in-flight write-tool confirmations in memory — no Postgres table, deliberately
// (see the plan this was built from): losing a pending confirmation on a rare server restart just
// means the user re-asks, which is an acceptable tradeoff for a personal app.
type pendingStore struct {
	mu    sync.Mutex
	items map[string]pendingAction
}

func newPendingStore() *pendingStore {
	return &pendingStore{items: make(map[string]pendingAction)}
}

func (s *pendingStore) store(a pendingAction) string {
	id := uuid.NewString()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[id] = a
	return id
}

// take removes and returns the pending action for id — single-use, so a double-click (or a replay)
// can't execute the same write twice. Returns ok=false if the id is unknown or expired.
func (s *pendingStore) take(id string) (pendingAction, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.items[id]
	if !ok {
		return pendingAction{}, false
	}
	delete(s.items, id)
	if time.Since(a.CreatedAt) > pendingActionTTL {
		return pendingAction{}, false
	}
	return a, true
}
