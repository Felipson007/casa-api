package pandora

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ToolDef is one OpenAI-format tool definition sent in a ChatCompletion request.
type ToolDef struct {
	Type     string      `json:"type"` // always "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef describes one callable function — Parameters is a JSON Schema object.
type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolCallOut is one entry of a ChatCompletionMessage.ToolCalls — the model's request to call a
// function, per the OpenAI tool-calling protocol. Arguments is a JSON-encoded string (the spec's
// own format, not a decoded object) — callers json.Unmarshal it themselves.
type ToolCallOut struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	// ExtraContent carries provider-specific metadata that must be echoed back verbatim on the
	// next turn. Gemini's OpenAI-compatible endpoint returns a thinking model's cryptographic
	// thought_signature here ({"google":{"thought_signature":"…"}}) and rejects any follow-up
	// request that replays the assistant's functionCall without it (HTTP 400 "Function call is
	// missing a thought_signature"). Kept as raw JSON so whatever the provider puts there round-
	// trips untouched; non-Gemini providers simply omit it.
	ExtraContent json.RawMessage `json:"extra_content,omitempty"`
}

// ChatCompletionMessage is one turn in a tool-calling conversation — a superset of ChatMessage
// (client.go) that also carries ToolCalls (assistant turns requesting a function call) and
// ToolCallID (tool-result turns answering one of those requests).
type ChatCompletionMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []ToolCallOut `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type completionRequest struct {
	Model    string                  `json:"model"`
	Messages []ChatCompletionMessage `json:"messages"`
	Tools    []ToolDef               `json:"tools,omitempty"`
	Stream   bool                    `json:"stream"`
}

type completionResponse struct {
	Choices []struct {
		Message ChatCompletionMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ChatCompletion sends a full multi-turn conversation (optionally with tool definitions) to the
// OmniRoute gateway and returns the assistant's reply message — which may itself carry ToolCalls
// instead of Content (see internal/assistant for the loop that executes them and calls back in).
// Stream is always explicitly false — see client.go's chatRequest doc comment for why.
func (s *Service) ChatCompletion(ctx context.Context, messages []ChatCompletionMessage, tools []ToolDef) (ChatCompletionMessage, error) {
	conn, err := s.resolve(ctx)
	if err != nil {
		return ChatCompletionMessage{}, err
	}

	payload, err := json.Marshal(completionRequest{Model: conn.ChatModel, Messages: messages, Tools: tools})
	if err != nil {
		return ChatCompletionMessage{}, fmt.Errorf("pandora: marshal completion request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, conn.BaseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ChatCompletionMessage{}, fmt.Errorf("pandora: build completion request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyAuth(req, conn)

	resp, err := s.clientFor(ctx).Do(req)
	if err != nil {
		return ChatCompletionMessage{}, fmt.Errorf("pandora indisponível: %w", err)
	}
	defer resp.Body.Close()

	body, err := readGatewayResponse(resp.Body)
	if err != nil {
		return ChatCompletionMessage{}, fmt.Errorf("pandora: read completion response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return ChatCompletionMessage{}, fmt.Errorf("pandora: status %d: %s", resp.StatusCode, string(body))
	}

	var parsed completionResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ChatCompletionMessage{}, fmt.Errorf("pandora: decode completion response: %w", err)
	}
	if parsed.Error != nil {
		return ChatCompletionMessage{}, fmt.Errorf("pandora: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return ChatCompletionMessage{}, fmt.Errorf("pandora: resposta vazia")
	}
	return parsed.Choices[0].Message, nil
}
