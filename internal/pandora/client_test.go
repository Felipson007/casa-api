package pandora

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadDocumentBuildsMultimodalRequest(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		contentType string
		partType    string
		dataPrefix  string
	}{
		{name: "image", filename: "receipt.png", contentType: "image/png", partType: "image_url", dataPrefix: "data:image/png;base64,"},
		{name: "scanned PDF", filename: "invoice.pdf", contentType: "application/pdf", partType: "file", dataPrefix: "data:application/pdf;base64,"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requestBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					t.Fatalf("unexpected path %q", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer secret" {
					t.Fatalf("missing API key")
				}
				body, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(body, &requestBody); err != nil {
					t.Fatalf("invalid request JSON: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"recognized text"}}]}`))
			}))
			defer server.Close()

			service := NewService(NewStaticResolver(Connector{BaseURL: server.URL, APIKey: "secret", ChatModel: "vision-model"}))
			text, err := service.ReadDocument(context.Background(), tt.filename, tt.contentType, []byte("binary"))
			if err != nil || text != "recognized text" {
				t.Fatalf("ReadDocument() = %q, %v", text, err)
			}
			messages := requestBody["messages"].([]any)
			content := messages[0].(map[string]any)["content"].([]any)
			part := content[1].(map[string]any)
			if part["type"] != tt.partType {
				t.Fatalf("part type = %v, want %s", part["type"], tt.partType)
			}
			encoded := ""
			if tt.partType == "file" {
				file := part["file"].(map[string]any)
				encoded, _ = file["file_data"].(string)
				if file["filename"] != tt.filename {
					t.Fatalf("filename = %v", file["filename"])
				}
			} else {
				image := part["image_url"].(map[string]any)
				encoded, _ = image["url"].(string)
			}
			if !strings.HasPrefix(encoded, tt.dataPrefix) {
				t.Fatalf("data URI %q does not start with %q", encoded, tt.dataPrefix)
			}
		})
	}
}

func TestReadGatewayResponseLimitsBody(t *testing.T) {
	_, err := readGatewayResponse(strings.NewReader(strings.Repeat("x", maxGatewayResponseBytes+1)))
	if err == nil {
		t.Fatal("readGatewayResponse() accepted an oversized response")
	}

	data, err := readGatewayResponse(strings.NewReader("ok"))
	if err != nil || string(data) != "ok" {
		t.Fatalf("readGatewayResponse() = %q, %v", data, err)
	}
}

func TestChatMultiWrapsErrRateLimitedOn429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
	}))
	defer server.Close()

	service := NewService(NewStaticResolver(Connector{BaseURL: server.URL, APIKey: "secret", ChatModel: "model"}))
	_, err := service.ChatMulti(context.Background(), "", []ChatMessage{{Role: "user", Content: "oi"}})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("ChatMulti() error = %v, want errors.Is(err, ErrRateLimited)", err)
	}
}

// TestChatMultiWrapsErrModelOverloadedOn503 regresses production 2026-09-17: a real exam upload
// hit a 503 "model overloaded" reply from the gateway, which fell into the generic "pandora:
// status %d" error and staged the document with a note wrongly implying the file itself was
// unreadable.
func TestChatMultiWrapsErrModelOverloadedOn503(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"[gemini/gemini-3.6-flash] [503]: This model is currently experiencing high demand."}}`))
	}))
	defer server.Close()

	service := NewService(NewStaticResolver(Connector{BaseURL: server.URL, APIKey: "secret", ChatModel: "model"}))
	_, err := service.ChatMulti(context.Background(), "", []ChatMessage{{Role: "user", Content: "oi"}})
	if !errors.Is(err, ErrModelOverloaded) {
		t.Fatalf("ChatMulti() error = %v, want errors.Is(err, ErrModelOverloaded)", err)
	}
}

func TestReadDocumentWrapsErrModelOverloadedOn503(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"high demand"}}`))
	}))
	defer server.Close()

	service := NewService(NewStaticResolver(Connector{BaseURL: server.URL, APIKey: "secret", ChatModel: "model"}))
	_, err := service.ReadDocument(context.Background(), "laudo.pdf", "application/pdf", []byte("%PDF-1.4"))
	if !errors.Is(err, ErrModelOverloaded) {
		t.Fatalf("ReadDocument() error = %v, want errors.Is(err, ErrModelOverloaded)", err)
	}
}

// TestChatCompletionRoundTripsToolCallExtraContent regresses a tenant-level bug: a Gemini thinking
// model (e.g. gemini-3.5-flash) reached through Google's OpenAI-compatible endpoint attaches its
// cryptographic thought_signature to the functionCall as
// extra_content.google.thought_signature. The assistant loop appends the model reply verbatim and
// calls back with the tool output, so that metadata must survive the round-trip — otherwise Gemini
// rejects the follow-up turn with 400 "Function call is missing a thought_signature".
func TestChatCompletionRoundTripsToolCallExtraContent(t *testing.T) {
	var secondRequest map[string]any
	call := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"gastos_listar","arguments":"{}"},"extra_content":{"google":{"thought_signature":"SIG-ABC"}}}]}}]}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &secondRequest)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	service := NewService(NewStaticResolver(Connector{BaseURL: server.URL, APIKey: "secret", ChatModel: "gemini-3.5-flash"}))
	user := ChatCompletionMessage{Role: "user", Content: "quanto gastei?"}

	reply, err := service.ChatCompletion(context.Background(), []ChatCompletionMessage{user}, nil)
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if len(reply.ToolCalls) != 1 || !strings.Contains(string(reply.ToolCalls[0].ExtraContent), `"thought_signature":"SIG-ABC"`) {
		t.Fatalf("reply tool call extra_content = %s, want the thought_signature preserved", reply.ToolCalls[0].ExtraContent)
	}

	messages := []ChatCompletionMessage{user, reply, {Role: "tool", Content: `{"total":0}`, ToolCallID: "call-1"}}
	if _, err := service.ChatCompletion(context.Background(), messages, nil); err != nil {
		t.Fatalf("second ChatCompletion() error = %v", err)
	}

	msgs, _ := secondRequest["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("second request carried %d messages, want 3", len(msgs))
	}
	assistant, _ := msgs[1].(map[string]any)
	tcs, _ := assistant["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("replayed assistant tool_calls = %v", assistant["tool_calls"])
	}
	tc, _ := tcs[0].(map[string]any)
	ec, _ := tc["extra_content"].(map[string]any)
	google, _ := ec["google"].(map[string]any)
	if google["thought_signature"] != "SIG-ABC" {
		t.Fatalf("replayed extra_content = %v, want thought_signature SIG-ABC", tc["extra_content"])
	}
}
