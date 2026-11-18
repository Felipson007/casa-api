package chat

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// RegisterRoutes mounts this module's endpoints on mux. Composition happens in cmd/api/main.go
// (Hermes-owned per D-005). Every handler is gated by httpx.RequirePerm (D-003), module "chat" —
// see internal/expenses/routes.go for the reference pattern. Only 3 routes, matching the real
// Java contract (ChatController): no /{id} routes exist there either.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	const module = "chat"
	mux.HandleFunc("GET /api/chat/messages", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermRead, handler.List)))
	mux.HandleFunc("POST /api/chat/messages", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.Create)))
	mux.HandleFunc("DELETE /api/chat/messages", httpx.Wrap(httpx.RequirePerm(httpx.DefaultCheckPerm, module, httpx.PermWrite, handler.DeleteAll)))
}
