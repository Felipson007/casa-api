package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/categories"
	"github.com/atlas/atlas-go/internal/chat"
	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/finance"
	"github.com/atlas/atlas-go/internal/goals"
	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/pandora"
	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// maxToolIterations caps the tool-calling loop per Reply/Resume call — a model that keeps
// requesting tools without ever producing a final answer stops here (as an error) instead of
// looping forever. Reset on every Resume, so it bounds each leg of a multi-confirmation exchange,
// not the whole conversation.
const maxToolIterations = 5

// buildSystemPrompt is a func, not a const: the model has no clock of its own — without today's
// real date spelled out, it guesses (confirmed in practice: asked about "este mês" in 2026, it
// silently assumed 2025 and returned an empty result instead of asking or erroring). civildate.
// Today(), not time.Now() — see that package's doc comment on the UTC-vs-America/Sao_Paulo
// day-rollover skew a bare time.Now() would introduce here.
func buildSystemPrompt() string {
	hoje := civildate.Today().Time().Format("2006-01-02")
	return fmt.Sprintf(`Você é o assistente pessoal do Atlas, um app de gestão financeira e familiar.
Hoje é %s (AAAA-MM-DD). Use essa data como referência real para "hoje", "este mês", "esse ano" etc.
— nunca assuma o ano ou mês atual por conta própria.

Responda sempre no mesmo idioma da última mensagem do usuário — se ele escrever em inglês,
responda em inglês; se escrever em português, responda em português; e assim por diante. Não
troque de idioma no meio da conversa por conta própria, só quando o usuário trocar primeiro. Nomes
próprios, valores monetários e termos técnicos (ex.: nomes de ferramentas) permanecem como estão,
independente do idioma. Use as ferramentas disponíveis para consultar dados reais antes de
responder — o Atlas cobre finanças (empréstimos, gastos, receitas, assinaturas e saldos), família
e saúde. Nunca invente valores, e nunca invente um UUID: sempre obtenha ids reais chamando
uma ferramenta de listagem primeiro. Ferramentas que alteram dados
pausam automaticamente para confirmação do usuário — você não precisa (e não deve tentar) pedir essa
confirmação você mesmo em texto, apenas chame a ferramenta normalmente. Se uma pergunta não puder
ser respondida com as ferramentas disponíveis, diga isso claramente. Seja conciso.

Evite loops de ferramentas: para perguntas sobre "o mês", "este mês" ou um mês específico
envolvendo várias parcelas, use emprestimos_vigentes_mes (uma única chamada cobre todos os
empréstimos) em vez de chamar emprestimos_parcelas uma vez por empréstimo — isso é caro e lento.
Reserve emprestimos_parcelas para quando o usuário pedir o cronograma completo de UM empréstimo
específico.`, hoje)
}

// Service orchestrates the "IA Atlas" chat: persists messages, runs the tool-calling loop against
// OmniRoute, and pauses write tools for explicit user confirmation (see pending.go) before ever
// executing one.
type Service struct {
	chatSvc  *chat.Service
	pandora  *pandora.Service
	tools    map[string]toolDef
	toolDefs []pandora.ToolDef
	pending  *pendingStore
}

func NewService(chatSvc *chat.Service, pandoraSvc *pandora.Service, loansSvc *loans.Service, peopleSvc *people.Service, healthSvc *health.Service, expensesSvc *expenses.Service, incomeSvc *income.Service, financeSvc *finance.Service, goalsSvc *goals.Service, categoriesSvc *categories.Service) *Service {
	tools := buildTools(loansSvc, peopleSvc, healthSvc, expensesSvc, incomeSvc, financeSvc, goalsSvc, categoriesSvc)
	defs := make([]pandora.ToolDef, 0, len(tools))
	for name, t := range tools {
		defs = append(defs, pandora.ToolDef{
			Type:     "function",
			Function: pandora.FunctionDef{Name: name, Description: t.description, Parameters: t.parameters},
		})
	}
	return &Service{chatSvc: chatSvc, pandora: pandoraSvc, tools: tools, toolDefs: defs, pending: newPendingStore()}
}

// Reply persists appUserID's userText as a USER message, then runs the tool-calling loop. Returns
// either the persisted ASSISTANT reply, or a PendingConfirmation if the loop paused on a write
// tool — never both. onToolCall is invoked once per read tool executed, in call order (nil-safe —
// pass nil to skip progress reporting; handler.go uses it for SSE progress events).
func (s *Service) Reply(ctx context.Context, appUserID uuid.UUID, userText string, onToolCall func(name string)) (chat.MessageDTO, *PendingConfirmation, error) {
	if _, err := s.chatSvc.Create(ctx, appUserID, chat.CreateMessageRequest{Role: chat.RoleUser, Content: userText}); err != nil {
		return chat.MessageDTO{}, nil, fmt.Errorf("assistant: persisting user message: %w", err)
	}

	history, err := s.chatSvc.List(ctx, appUserID)
	if err != nil {
		return chat.MessageDTO{}, nil, fmt.Errorf("assistant: loading history: %w", err)
	}

	messages := make([]pandora.ChatCompletionMessage, 0, len(history)+1)
	messages = append(messages, pandora.ChatCompletionMessage{Role: "system", Content: buildSystemPrompt()})
	for _, m := range history {
		messages = append(messages, pandora.ChatCompletionMessage{Role: roleToPandora(m.Role), Content: m.Content})
	}

	return s.runLoop(ctx, appUserID, messages, nil, onToolCall)
}

// Resume continues a turn that paused on a write-tool confirmation. approved=false persists a
// short note instead of executing anything; approved=true executes the pending tool, feeds its
// result back in, and continues the same loop — which may pause again on a further write tool.
func (s *Service) Resume(ctx context.Context, appUserID uuid.UUID, pendingID string, approved bool, onToolCall func(name string)) (chat.MessageDTO, *PendingConfirmation, error) {
	pending, ok := s.pending.take(pendingID)
	if !ok || pending.AppUserID != appUserID {
		return chat.MessageDTO{}, nil, fmt.Errorf("assistant: confirmação não encontrada ou expirada")
	}

	if !approved {
		msg, err := s.chatSvc.Create(ctx, appUserID, chat.CreateMessageRequest{
			Role:      chat.RoleAssistant,
			Content:   "Ação cancelada a pedido do usuário.",
			ToolCalls: pending.CalledTools,
		})
		if err != nil {
			return chat.MessageDTO{}, nil, fmt.Errorf("assistant: persisting cancellation: %w", err)
		}
		return msg, nil, nil
	}

	var args map[string]any
	_ = json.Unmarshal([]byte(pending.ToolCall.Function.Arguments), &args)
	if onToolCall != nil {
		onToolCall(pending.ToolCall.Function.Name)
	}
	result, execErr := s.runTool(ctx, pending.ToolCall.Function.Name, args)
	if execErr != nil {
		result = fmt.Sprintf(`{"error":%q}`, execErr.Error())
	}
	calledTools := append(pending.CalledTools, chat.ToolCall{Name: pending.ToolCall.Function.Name, Arguments: args})
	messages := append(pending.Messages, pandora.ChatCompletionMessage{Role: "tool", Content: result, ToolCallID: pending.ToolCall.ID})

	return s.runLoop(ctx, appUserID, messages, calledTools, onToolCall)
}

// runLoop drives the model round-trips: executes read tools immediately, but stops and returns a
// PendingConfirmation the instant it sees a write tool — any further tool calls in that same batch
// are left unprocessed (the model can re-request them once the pending one is resolved). Shared by
// Reply (fresh messages) and Resume (continuing from a resolved confirmation).
func (s *Service) runLoop(ctx context.Context, appUserID uuid.UUID, messages []pandora.ChatCompletionMessage, calledTools []chat.ToolCall, onToolCall func(name string)) (chat.MessageDTO, *PendingConfirmation, error) {
	for i := 0; i < maxToolIterations; i++ {
		reply, err := s.pandora.ChatCompletion(ctx, messages, s.toolDefs)
		if err != nil {
			return chat.MessageDTO{}, nil, fmt.Errorf("assistant: chat completion: %w", err)
		}

		if len(reply.ToolCalls) == 0 {
			assistantMsg, err := s.chatSvc.Create(ctx, appUserID, chat.CreateMessageRequest{
				Role:      chat.RoleAssistant,
				Content:   reply.Content,
				ToolCalls: calledTools,
			})
			if err != nil {
				return chat.MessageDTO{}, nil, fmt.Errorf("assistant: persisting assistant message: %w", err)
			}
			return assistantMsg, nil, nil
		}

		messages = append(messages, reply)
		for _, tc := range reply.ToolCalls {
			def, known := s.tools[tc.Function.Name]
			if known && def.write {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				id := s.pending.store(pendingAction{
					AppUserID:   appUserID,
					Messages:    messages,
					CalledTools: calledTools,
					ToolCall:    tc,
					CreatedAt:   time.Now(),
				})
				return chat.MessageDTO{}, &PendingConfirmation{ID: id, ToolName: tc.Function.Name, Arguments: args}, nil
			}

			if onToolCall != nil {
				onToolCall(tc.Function.Name)
			}
			var args map[string]any
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			result, execErr := s.runTool(ctx, tc.Function.Name, args)
			if execErr != nil {
				result = fmt.Sprintf(`{"error":%q}`, execErr.Error())
			}
			calledTools = append(calledTools, chat.ToolCall{Name: tc.Function.Name, Arguments: args})
			messages = append(messages, pandora.ChatCompletionMessage{Role: "tool", Content: result, ToolCallID: tc.ID})
		}
	}
	return chat.MessageDTO{}, nil, fmt.Errorf("assistant: excedeu %d chamadas de ferramenta sem uma resposta final", maxToolIterations)
}

func (s *Service) runTool(ctx context.Context, name string, args map[string]any) (string, error) {
	t, ok := s.tools[name]
	if !ok {
		return "", fmt.Errorf("assistant: ferramenta desconhecida %q", name)
	}
	return t.run(ctx, args)
}

func roleToPandora(r chat.Role) string {
	switch r {
	case chat.RoleAssistant:
		return "assistant"
	case chat.RoleSystem:
		return "system"
	default:
		return "user"
	}
}
