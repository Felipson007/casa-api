package pandora

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrRateLimited wraps a gateway response of HTTP 429 — distinct from every other failure because
// it resolves itself in a minute or two rather than needing the user to retry immediately or
// re-upload a file, which is what the generic "pandora: status %d" message otherwise implies.
// Callers (assistant.Handler, documents.Service) check errors.Is(err, ErrRateLimited) to swap in a
// message that says "wait a bit" instead of "something is wrong".
var ErrRateLimited = errors.New("pandora: limite de requisições atingido")

// ErrModelOverloaded wraps a gateway response of HTTP 503 — a specific model reporting it's
// temporarily overloaded ("high demand", "reset after Ns"), same "wait a bit, don't re-upload"
// shape as ErrRateLimited but a different cause (the model itself, not the caller's request rate).
// Confirmed in production, 2026-09-17: a real exam upload hit this on gemini-3.6-flash and staged
// with the generic "couldn't read this document" note, which wrongly pointed at the file instead
// of the transient outage.
var ErrModelOverloaded = errors.New("pandora: modelo temporariamente sobrecarregado")

// ErrNoProvider means the context's tenant has no AI connector configured. There is no env
// fallback, so this is a client-fixable condition (configure a provider), not a server error —
// handlers map it to a 4xx with a clear message.
var ErrNoProvider = errors.New("pandora: nenhum provedor de IA configurado para este tenant")

type multimodalMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type multimodalContentPart struct {
	Type     string              `json:"type"`
	Text     string              `json:"text,omitempty"`
	ImageURL *multimodalImageURL `json:"image_url,omitempty"`
	File     *multimodalFile     `json:"file,omitempty"`
}

type multimodalImageURL struct {
	URL string `json:"url"`
}

type multimodalFile struct {
	Filename string `json:"filename"`
	FileData string `json:"file_data"`
}

type multimodalRequest struct {
	Model    string              `json:"model"`
	Messages []multimodalMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

// defaultChatTimeout bounds one gateway completion call when the caller sets no deadline of its
// own — see ChatMulti for why a caller with a longer deadline overrides it.
const defaultChatTimeout = 3 * time.Minute

const maxGatewayResponseBytes = 8 << 20

func readGatewayResponse(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(body, maxGatewayResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > maxGatewayResponseBytes {
		return nil, fmt.Errorf("resposta excede o limite de %d bytes", maxGatewayResponseBytes)
	}
	return data, nil
}

// clientFor returns a shallow copy so a request with a caller-owned longer deadline can extend
// the client timeout without mutating the shared client. The transport remains shared, allowing
// connection reuse across chat and document-extraction calls.
func (s *Service) clientFor(ctx context.Context) *http.Client {
	client := *s.httpClient
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > client.Timeout {
			client.Timeout = remaining
		}
	}
	return &client
}

// ChatMessage is one turn of an OpenAI-format chat completion request.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest is the minimal OpenAI-compatible payload we send to the gateway. Stream is always
// explicitly false — some gateway combos default to SSE streaming when the field is simply
// omitted, which breaks the single-JSON-object parsing below (confirmed against OmniRoute's
// pandora-gateway combo: omitting `stream` returns `data: {...}` chunks instead of one object).
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

// chatResponse mirrors the OpenAI response shape (only the fields we read).
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends a system+user prompt to the OmniRoute gateway's /v1/chat/completions and returns the
// assistant text. Backend-to-gateway call (not the browser proxy path). model is optional — when
// empty, OMNIROUTE_MODEL is used.
func (s *Service) Chat(ctx context.Context, model string, system, user string) (string, error) {
	var messages []ChatMessage
	if system != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: system})
	}
	messages = append(messages, ChatMessage{Role: "user", Content: user})
	return s.ChatMulti(ctx, model, messages)
}

// ChatMulti is Chat for a full message history (system + however many prior turns) instead of a
// single system+user pair — the shape the dashboard's chat widget needs, since it keeps
// conversation state client-side and resends the whole thing each turn (see
// pandora.Handler.DashboardChat, the one caller so far).
func (s *Service) ChatMulti(ctx context.Context, model string, messages []ChatMessage) (string, error) {
	conn, err := s.resolve(ctx)
	if err != nil {
		return "", err
	}
	if model == "" {
		model = conn.ChatModel
	}
	reqBody := chatRequest{Model: model, Messages: messages}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("pandora: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, conn.BaseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("pandora: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyAuth(req, conn)

	// The 3-minute default suits interactive chat, where a caller waiting on an HTTP response
	// would rather see an error than hang. Background work (document extraction) legitimately
	// takes longer — a full credit-card statement's structured extraction routinely runs past
	// three minutes and was failing outright on the timeout — so a caller that has set its own,
	// longer deadline gets that instead of being cut short by this default.
	resp, err := s.clientFor(ctx).Do(req)
	if err != nil {
		return "", fmt.Errorf("pandora indisponível: %w", err)
	}
	defer resp.Body.Close()

	body, err := readGatewayResponse(resp.Body)
	if err != nil {
		return "", fmt.Errorf("pandora: read response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("%w: %s", ErrRateLimited, string(body))
	}
	if resp.StatusCode == http.StatusServiceUnavailable {
		return "", fmt.Errorf("%w: %s", ErrModelOverloaded, string(body))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pandora: status %d: %s", resp.StatusCode, string(body))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("pandora: decode response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("pandora: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("pandora: resposta vazia")
	}
	return parsed.Choices[0].Message.Content, nil
}

// ReadDocument asks the configured multimodal model to transcribe a document. Images use an
// image_url content part; PDFs use a file content part so scanned pages can also be inspected.
func (s *Service) ReadDocument(ctx context.Context, filename, contentType string, content []byte) (string, error) {
	conn, err := s.resolve(ctx)
	if err != nil {
		return "", err
	}
	model := conn.ExtractionModel
	if model == "" {
		model = conn.ChatModel
	}

	encoded := base64.StdEncoding.EncodeToString(content)
	parts := []multimodalContentPart{{
		Type: "text",
		Text: "Transcreva todo o texto visível deste documento, preservando datas, valores, tabelas e a ordem das linhas. Responda somente com o texto transcrito, sem comentários.",
	}}
	if contentType == "application/pdf" {
		parts = append(parts, multimodalContentPart{
			Type: "file",
			File: &multimodalFile{Filename: filename, FileData: "data:application/pdf;base64," + encoded},
		})
	} else {
		parts = append(parts, multimodalContentPart{
			Type:     "image_url",
			ImageURL: &multimodalImageURL{URL: "data:" + contentType + ";base64," + encoded},
		})
	}

	payload, err := json.Marshal(multimodalRequest{
		Model: model, Messages: []multimodalMessage{{Role: "user", Content: parts}},
	})
	if err != nil {
		return "", fmt.Errorf("pandora: marshal document request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, conn.BaseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("pandora: build document request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyAuth(req, conn)

	resp, err := s.clientFor(ctx).Do(req)
	if err != nil {
		return "", fmt.Errorf("pandora indisponível: %w", err)
	}
	defer resp.Body.Close()
	body, err := readGatewayResponse(resp.Body)
	if err != nil {
		return "", fmt.Errorf("pandora: read document response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("%w: %s", ErrRateLimited, string(body))
	}
	if resp.StatusCode == http.StatusServiceUnavailable {
		return "", fmt.Errorf("%w: %s", ErrModelOverloaded, string(body))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pandora: document status %d: %s", resp.StatusCode, string(body))
	}
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("pandora: decode document response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("pandora: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("pandora: resposta de documento vazia")
	}
	return parsed.Choices[0].Message.Content, nil
}
