package assistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/atlas/atlas-go/internal/chat"
	"github.com/atlas/atlas-go/internal/pandora"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler streams one chat turn (or the resumption of a paused one) over SSE. Contract matches the
// frontend's lib/api/chat.ts: event "ferramenta" per read tool executed ({"nome": "..."}), event
// "confirmacao" when the turn pauses on a write tool ({"pendingId", "ferramenta", "argumentos"}),
// event "final" with the persisted chat.MessageDTO, event "erro" on failure. Exactly one of
// "confirmacao"/"final"/"erro" is emitted per request — the stream always ends right after.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// streamRequest accepts two mutually-exclusive shapes: {text} starts a new turn, {pendingId,
// aprovado} resumes one paused on a write-tool confirmation.
type streamRequest struct {
	Text      *string `json:"text"`
	PendingID *string `json:"pendingId"`
	Aprovado  *bool   `json:"aprovado"`
}

// Stream handles POST /api/chat/stream.
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) error {
	u, ok := httpx.UserFromContext(r.Context())
	if !ok {
		return apperr.Unauthorized("no authenticated user in context")
	}

	req, err := httpx.DecodeAndValidate[streamRequest](r)
	if err != nil {
		return err
	}
	isNewTurn := req.Text != nil && *req.Text != ""
	isResume := req.PendingID != nil && *req.PendingID != "" && req.Aprovado != nil
	if !isNewTurn && !isResume {
		return apperr.Validation("either text, or pendingId+aprovado, is required")
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		return apperr.Internal(fmt.Errorf("assistant: response writer does not support flushing"))
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	writeEvent := func(event string, data any) {
		payload, err := json.Marshal(data)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
		flusher.Flush()
	}
	onToolCall := func(name string) {
		writeEvent("ferramenta", map[string]string{"nome": name})
	}

	var (
		reply   chat.MessageDTO
		pending *PendingConfirmation
		runErr  error
	)
	if isNewTurn {
		reply, pending, runErr = h.svc.Reply(r.Context(), u.ID, *req.Text, onToolCall)
	} else {
		reply, pending, runErr = h.svc.Resume(r.Context(), u.ID, *req.PendingID, *req.Aprovado, onToolCall)
	}

	if runErr != nil {
		message := runErr.Error()
		switch {
		case errors.Is(runErr, pandora.ErrRateLimited):
			message = "O limite de requisições da IA foi atingido. Aguarde 1-2 minutos e tente novamente."
		case errors.Is(runErr, pandora.ErrModelOverloaded):
			message = "O modelo de IA está temporariamente sobrecarregado. Aguarde alguns instantes e tente novamente."
		case errors.Is(runErr, pandora.ErrNoProvider):
			message = "Nenhum provedor de IA configurado para este espaço. Configure um em Configurações."
		}
		writeEvent("erro", map[string]string{"mensagem": message})
		return nil
	}
	if pending != nil {
		writeEvent("confirmacao", map[string]any{
			"pendingId":  pending.ID,
			"ferramenta": pending.ToolName,
			"argumentos": pending.Arguments,
		})
		return nil
	}
	writeEvent("final", reply)
	return nil
}
