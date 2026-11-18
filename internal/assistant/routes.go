package assistant

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts the assistant's streaming endpoint on mux. Gated under module "chat" —
// same permission as /api/chat/messages, since this is the orchestration layer that module's own
// doc comment anticipated, not a separate feature area.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "chat"
	mux.HandleFunc("POST /api/chat/stream", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Stream)))
}
