// Package assistant is the "IA Atlas" AI-orchestration layer internal/chat's own doc comment
// anticipated: it runs the tool-calling loop against OmniRoute (via internal/pandora) and persists
// both turns through internal/chat. Read tools execute immediately; write tools (toolDef.write)
// always pause for explicit user confirmation first — see pending.go and service.go's runLoop.
package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/categories"
	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/finance"
	"github.com/atlas/atlas-go/internal/goals"
	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/personrelations"
	"github.com/atlas/atlas-go/internal/pets"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// toolDef pairs one tool's OpenAI-format description/parameters with the function that actually
// runs it. run's return value is already JSON-encoded — the exact string sent back to the model as
// the "tool" role message content. write marks a tool as mutating — runLoop never executes one of
// these without first pausing for user confirmation (see pending.go).
type toolDef struct {
	description string
	parameters  map[string]any
	write       bool
	run         func(ctx context.Context, args map[string]any) (string, error)
}

// buildTools binds the tool registry to the given services.
func buildTools(loansSvc *loans.Service, peopleSvc *people.Service, petsSvc *pets.Service, healthSvc *health.Service, expensesSvc *expenses.Service, incomeSvc *income.Service, financeSvc *finance.Service, goalsSvc *goals.Service, categoriesSvc *categories.Service) map[string]toolDef {
	noArgs := map[string]any{"type": "object", "properties": map[string]any{}}
	uuidArg := func(name, description string) map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				name: map[string]any{"type": "string", "description": description},
			},
			"required": []string{name},
		}
	}

	return map[string]toolDef{
		// ── Leitura ──────────────────────────────────────────────────────────────────────────
		"emprestimos_listar": {
			description: "Lista todos os empréstimos, ativos e encerrados.",
			parameters:  noArgs,
			run: func(ctx context.Context, args map[string]any) (string, error) {
				return marshalResult(loansSvc.List(ctx, nil, ""))
			},
		},
		"emprestimos_buscar": {
			description: "Busca um empréstimo específico pelo id.",
			parameters:  uuidArg("id", "UUID do empréstimo"),
			run: func(ctx context.Context, args map[string]any) (string, error) {
				id, err := requireUUID(args, "id")
				if err != nil {
					return "", err
				}
				return marshalResult(loansSvc.Get(ctx, id))
			},
		},
		"emprestimos_parcelas": {
			description: "Lista as parcelas (cronograma de pagamento) de um empréstimo específico.",
			parameters:  uuidArg("emprestimo_id", "UUID do empréstimo"),
			run: func(ctx context.Context, args map[string]any) (string, error) {
				id, err := requireUUID(args, "emprestimo_id")
				if err != nil {
					return "", err
				}
				return marshalResult(loansSvc.ListInstallments(ctx, id))
			},
		},
		"emprestimos_movimentacoes": {
			description: "Lista o extrato de movimentações (pagamentos, desembolsos, amortizações) de todos os empréstimos.",
			parameters:  noArgs,
			run: func(ctx context.Context, args map[string]any) (string, error) {
				return marshalResult(loansSvc.ListAllTransactions(ctx, nil))
			},
		},
		"emprestimos_vigentes_mes": {
			description: "Resumo de TODOS os empréstimos num mês específico — status da parcela (paga/pendente/atrasada/quitado), valor, data de vencimento e cliente, um por empréstimo. Uma única consulta cobrindo todos os empréstimos: use esta ferramenta para perguntas sobre 'o mês' ou 'este mês' em vez de chamar emprestimos_parcelas repetidamente para cada empréstimo.",
			parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ano": map[string]any{"type": "integer", "description": "Ano, ex. 2026"},
					"mes": map[string]any{"type": "integer", "description": "Mês, 1 a 12"},
				},
				"required": []string{"ano", "mes"},
			},
			run: func(ctx context.Context, args map[string]any) (string, error) {
				ano, err := requireInt(args, "ano")
				if err != nil {
					return "", err
				}
				mes, err := requireInt(args, "mes")
				if err != nil {
					return "", err
				}
				ym := loans.YearMonth{Year: ano, Month: mes}
				result, err := loansSvc.Vigentes(ctx, []loans.YearMonth{ym})
				if err != nil {
					return "", err
				}
				return marshalResult(result[ym], nil)
			},
		},
		"pessoas_listar": {
			description: "Lista todas as pessoas da família cadastradas.",
			parameters:  noArgs,
			run: func(ctx context.Context, args map[string]any) (string, error) {
				return marshalResult(peopleSvc.ListPeople(ctx, ""))
			},
		},
		"pessoas_parentesco": {
			description: "Retorna o grau de parentesco de cada pessoa cadastrada em relação a uma pessoa de origem.",
			parameters:  uuidArg("pessoa_id", "UUID da pessoa de origem"),
			run: func(ctx context.Context, args map[string]any) (string, error) {
				id, err := requireUUID(args, "pessoa_id")
				if err != nil {
					return "", err
				}
				return marshalResult(peopleSvc.DeriveFromOrigin(ctx, id))
			},
		},
		"pets_listar": {
			description: "Lista os pets da família.",
			parameters:  noArgs,
			run: func(ctx context.Context, args map[string]any) (string, error) {
				return marshalResult(petsSvc.List(ctx, nil))
			},
		},
		"saude_perfil": {
			description: "Retorna o perfil de saúde resumido (peso, altura, IMC e outras medidas mais recentes) de uma pessoa.",
			parameters:  uuidArg("pessoa_id", "UUID da pessoa"),
			run: func(ctx context.Context, args map[string]any) (string, error) {
				id, err := requireUUID(args, "pessoa_id")
				if err != nil {
					return "", err
				}
				return marshalResult(healthSvc.PerfilSaude(ctx, id))
			},
		},
		"saude_registros": {
			description: "Lista os registros de saúde de uma pessoa (exames, medicamentos, alergias, condições médicas, vacinas, etc.).",
			parameters:  uuidArg("pessoa_id", "UUID da pessoa"),
			run: func(ctx context.Context, args map[string]any) (string, error) {
				id, err := requireUUID(args, "pessoa_id")
				if err != nil {
					return "", err
				}
				return marshalResult(healthSvc.ListRecords(ctx, &id, nil))
			},
		},
		"gastos_listar": {
			description: "Lista TODAS as despesas registradas (descrição, valor, data, categoria, se é recorrente). Use para perguntas sobre um gasto específico por descrição (ex.: 'quanto gastei com Uber') ou por período — filtre o resultado você mesmo pela descrição/data.",
			parameters:  noArgs,
			run: func(ctx context.Context, args map[string]any) (string, error) {
				return marshalResult(expensesSvc.List(ctx, nil, nil, ""))
			},
		},
		"gastos_resumo_mensal": {
			description: "Resumo de despesas de um mês: total, contagem, quebra por categoria e por dia, maior e menor lançamento.",
			parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ano": map[string]any{"type": "integer", "description": "Ano, ex. 2026"},
					"mes": map[string]any{"type": "integer", "description": "Mês, 1 a 12"},
				},
				"required": []string{"ano", "mes"},
			},
			run: func(ctx context.Context, args map[string]any) (string, error) {
				ano, err := requireInt(args, "ano")
				if err != nil {
					return "", err
				}
				mes, err := requireInt(args, "mes")
				if err != nil {
					return "", err
				}
				return marshalResult(expensesSvc.MonthlySummary(ctx, ano, mes))
			},
		},
		"gastos_resumo_anual": {
			description: "Resumo de despesas de um ano inteiro: total, contagem, quebra por categoria e por mês, maior e menor lançamento.",
			parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ano": map[string]any{"type": "integer", "description": "Ano, ex. 2026"},
				},
				"required": []string{"ano"},
			},
			run: func(ctx context.Context, args map[string]any) (string, error) {
				ano, err := requireInt(args, "ano")
				if err != nil {
					return "", err
				}
				return marshalResult(expensesSvc.AnnualSummary(ctx, ano))
			},
		},
		"gastos_assinaturas": {
			description: "Lista as assinaturas detectadas (despesas recorrentes mensais estáveis): nome, valor atual, valor médio, última cobrança, próxima cobrança estimada.",
			parameters:  noArgs,
			run: func(ctx context.Context, args map[string]any) (string, error) {
				return marshalResult(expensesSvc.Subscriptions(ctx, time.Now()))
			},
		},
		"receitas_listar": {
			description: "Lista TODAS as receitas registradas (descrição, valor, data, categoria).",
			parameters:  noArgs,
			run: func(ctx context.Context, args map[string]any) (string, error) {
				return marshalResult(incomeSvc.List(ctx, nil, nil, ""))
			},
		},
		"saldos_listar": {
			description: "Lista os saldos financeiros cadastrados (contas, investimentos, reservas): nome, tipo, valor atual e data da última atualização.",
			parameters:  noArgs,
			run: func(ctx context.Context, args map[string]any) (string, error) {
				return marshalResult(financeSvc.List(ctx))
			},
		},
		"metas_listar": {
			description: "Lista as metas de planejamento financeiro (comprar algo, formar reserva, quitar dívida): valor-alvo, prazo, quanto já foi acumulado (incluindo saldos vinculados), quanto falta, progresso, meses estimados para atingir e se está no ritmo do prazo.",
			parameters:  noArgs,
			run: func(ctx context.Context, args map[string]any) (string, error) {
				return marshalResult(goalsSvc.List(ctx))
			},
		},
		"categorias_listar": {
			description: "Lista as categorias de gastos e receitas (id e nome) — use para traduzir os categoryId que aparecem em gastos_listar/receitas_listar.",
			parameters:  noArgs,
			run: func(ctx context.Context, args map[string]any) (string, error) {
				return marshalResult(categoriesSvc.List(ctx, nil))
			},
		},

		// ── Escrita — sempre pausa pra confirmação (ver pending.go/service.go) ─────────────────
		"emprestimos_marcar_parcela_paga": {
			description: "Marca uma parcela de empréstimo como paga.",
			write:       true,
			parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"emprestimo_id":  map[string]any{"type": "string", "description": "UUID do empréstimo"},
					"parcela_id":     map[string]any{"type": "string", "description": "UUID da parcela"},
					"valor_recebido": map[string]any{"type": "string", "description": "Valor recebido (opcional — default: valor total da parcela)"},
					"data_pagamento": map[string]any{"type": "string", "description": "Data do pagamento, formato YYYY-MM-DD (opcional — default: hoje)"},
				},
				"required": []string{"emprestimo_id", "parcela_id"},
			},
			run: func(ctx context.Context, args map[string]any) (string, error) {
				loanID, err := requireUUID(args, "emprestimo_id")
				if err != nil {
					return "", err
				}
				installmentID, err := requireUUID(args, "parcela_id")
				if err != nil {
					return "", err
				}
				paymentDate, err := optionalCivilDate(args, "data_pagamento")
				if err != nil {
					return "", err
				}
				req := loans.MarkInstallmentPaidRequest{
					AmountReceived: optionalString(args, "valor_recebido"),
					PaymentDate:    paymentDate,
				}
				return marshalResult(loansSvc.MarkInstallmentPaid(ctx, loanID, installmentID, req))
			},
		},
		"emprestimos_registrar_transacao": {
			description: "Registra uma transação avulsa num empréstimo: um pagamento não vinculado a uma parcela específica, ou o perdão de parte da dívida.",
			write:       true,
			parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"emprestimo_id":  map[string]any{"type": "string", "description": "UUID do empréstimo"},
					"tipo":           map[string]any{"type": "string", "enum": []string{"ONE_OFF_PAYMENT", "FORGIVENESS"}, "description": "ONE_OFF_PAYMENT = pagamento avulso, FORGIVENESS = perdão de dívida"},
					"valor":          map[string]any{"type": "string", "description": "Valor da transação"},
					"data_pagamento": map[string]any{"type": "string", "description": "Data, formato YYYY-MM-DD"},
					"descricao":      map[string]any{"type": "string", "description": "Descrição opcional"},
				},
				"required": []string{"emprestimo_id", "tipo", "valor", "data_pagamento"},
			},
			run: func(ctx context.Context, args map[string]any) (string, error) {
				loanID, err := requireUUID(args, "emprestimo_id")
				if err != nil {
					return "", err
				}
				tipo, err := requireString(args, "tipo")
				if err != nil {
					return "", err
				}
				valor, err := requireString(args, "valor")
				if err != nil {
					return "", err
				}
				dataStr, err := requireString(args, "data_pagamento")
				if err != nil {
					return "", err
				}
				data, err := parseCivilDate(dataStr)
				if err != nil {
					return "", err
				}
				req := loans.CreateTransactionRequest{
					Type:        loans.TransactionType(tipo),
					Amount:      valor,
					PaymentDate: data,
					Description: optionalString(args, "descricao"),
				}
				return marshalResult(loansSvc.RecordTransaction(ctx, loanID, req))
			},
		},
		"pessoas_criar": {
			description: "Cadastra uma nova pessoa na família.",
			write:       true,
			parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"nome":            map[string]any{"type": "string"},
					"apelido":         map[string]any{"type": "string"},
					"data_nascimento": map[string]any{"type": "string", "description": "Formato YYYY-MM-DD"},
					"genero":          map[string]any{"type": "string", "enum": []string{"M", "F"}},
					"notas":           map[string]any{"type": "string"},
					"email":           map[string]any{"type": "string"},
				},
				"required": []string{"nome"},
			},
			run: func(ctx context.Context, args map[string]any) (string, error) {
				nome, err := requireString(args, "nome")
				if err != nil {
					return "", err
				}
				birthDate, err := optionalCivilDate(args, "data_nascimento")
				if err != nil {
					return "", err
				}
				genero, _ := args["genero"].(string)
				req := people.CreatePersonRequest{
					Name:      nome,
					Nickname:  optionalString(args, "apelido"),
					BirthDate: birthDate,
					Gender:    personrelations.Gender(genero),
					Notes:     optionalString(args, "notas"),
					Email:     optionalString(args, "email"),
				}
				return marshalResult(peopleSvc.CreatePerson(ctx, req))
			},
		},
		"pets_criar": {
			description: "Cadastra um novo pet na família.",
			write:       true,
			parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"nome":            map[string]any{"type": "string"},
					"especie":         map[string]any{"type": "string", "enum": []string{"DOG", "CAT", "BIRD", "FISH", "OTHER"}},
					"raca":            map[string]any{"type": "string"},
					"data_nascimento": map[string]any{"type": "string", "description": "Formato YYYY-MM-DD"},
					"notas":           map[string]any{"type": "string"},
				},
				"required": []string{"nome", "especie"},
			},
			run: func(ctx context.Context, args map[string]any) (string, error) {
				nome, err := requireString(args, "nome")
				if err != nil {
					return "", err
				}
				especie, err := requireString(args, "especie")
				if err != nil {
					return "", err
				}
				birthDate, err := optionalCivilDate(args, "data_nascimento")
				if err != nil {
					return "", err
				}
				req := pets.CreatePetRequest{
					Name:      nome,
					Species:   pets.Species(especie),
					Breed:     optionalString(args, "raca"),
					BirthDate: birthDate,
					Notes:     optionalString(args, "notas"),
				}
				return marshalResult(petsSvc.Create(ctx, req))
			},
		},
		"saude_registro_criar": {
			description: "Registra um novo dado de saúde (peso, altura, exame, vacina, medicamento, alergia, condição médica, etc.) para uma pessoa.",
			write:       true,
			parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pessoa_id": map[string]any{"type": "string", "description": "UUID da pessoa"},
					"tipo": map[string]any{
						"type": "string",
						"enum": []string{
							"WEIGHT", "HEIGHT", "BLOOD_PRESSURE", "BODY_FAT_PCT", "MUSCLE_MASS", "WAIST_CIRC",
							"HIP_CIRC", "VISCERAL_FAT", "BMR", "LAB_RESULT", "ORGAN_ASSESSMENT", "VACCINE",
							"CHECKUP", "MEDICAL_CONDITION", "MEDICATION", "BLOOD_TYPE", "ALLERGY", "HABIT",
							"FAMILY_HISTORY", "NOTE",
						},
					},
					"data":             map[string]any{"type": "string", "description": "Formato YYYY-MM-DD"},
					"valor":            map[string]any{"type": "string", "description": "Valor numérico do registro (peso, altura, etc.), quando aplicável"},
					"unidade":          map[string]any{"type": "string"},
					"marcador_id":      map[string]any{"type": "string", "description": "UUID do marcador de exame do catálogo, quando aplicável"},
					"orgao":            map[string]any{"type": "string"},
					"status_avaliacao": map[string]any{"type": "string", "enum": []string{"NORMAL", "ATTENTION", "ALTERED"}},
					"notas":            map[string]any{"type": "string"},
				},
				"required": []string{"pessoa_id", "tipo", "data"},
			},
			run: func(ctx context.Context, args map[string]any) (string, error) {
				personID, err := requireUUID(args, "pessoa_id")
				if err != nil {
					return "", err
				}
				tipo, err := requireString(args, "tipo")
				if err != nil {
					return "", err
				}
				dataStr, err := requireString(args, "data")
				if err != nil {
					return "", err
				}
				data, err := parseCivilDate(dataStr)
				if err != nil {
					return "", err
				}
				markerID, err := optionalUUID(args, "marcador_id")
				if err != nil {
					return "", err
				}
				var assessmentStatus *health.AssessmentStatus
				if raw, _ := args["status_avaliacao"].(string); raw != "" {
					as := health.AssessmentStatus(raw)
					assessmentStatus = &as
				}
				req := health.CreateRecordRequest{
					PersonID:         personID,
					Type:             health.RecordType(tipo),
					RecordDate:       data,
					Amount:           optionalString(args, "valor"),
					Unit:             optionalString(args, "unidade"),
					MarkerCatalogID:  markerID,
					Organ:            optionalString(args, "orgao"),
					AssessmentStatus: assessmentStatus,
					Notes:            optionalString(args, "notas"),
				}
				return marshalResult(healthSvc.CreateRecord(ctx, req))
			},
		},
	}
}

// marshalResult JSON-encodes a service call's (result, error) pair as-is — lets every tool's run
// func be a one-liner `return marshalResult(svc.Method(ctx, ...))` regardless of the service
// error, propagated unchanged so the orchestration loop can surface it to the model as a tool
// error instead of a fabricated result.
func marshalResult(v any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	b, mErr := json.Marshal(v)
	if mErr != nil {
		return "", fmt.Errorf("assistant: encoding tool result: %w", mErr)
	}
	return string(b), nil
}

// requireUUID reads and parses a required string argument as a UUID.
func requireUUID(args map[string]any, key string) (uuid.UUID, error) {
	raw, _ := args[key].(string)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("assistant: argumento %q inválido: %w", key, err)
	}
	return id, nil
}

// optionalUUID parses args[key] as a UUID if present and non-empty, nil otherwise.
func optionalUUID(args map[string]any, key string) (*uuid.UUID, error) {
	raw, ok := args[key].(string)
	if !ok || raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("assistant: argumento %q inválido: %w", key, err)
	}
	return &id, nil
}

// requireInt reads a required numeric argument — JSON numbers decode into map[string]any as
// float64, never int, regardless of how the model formatted them.
func requireInt(args map[string]any, key string) (int, error) {
	raw, ok := args[key].(float64)
	if !ok {
		return 0, fmt.Errorf("assistant: argumento %q é obrigatório e deve ser numérico", key)
	}
	return int(raw), nil
}

// requireString reads a required, non-empty string argument.
func requireString(args map[string]any, key string) (string, error) {
	raw, ok := args[key].(string)
	if !ok || raw == "" {
		return "", fmt.Errorf("assistant: argumento %q é obrigatório", key)
	}
	return raw, nil
}

// optionalString reads args[key] as *string, nil if absent or empty.
func optionalString(args map[string]any, key string) *string {
	raw, ok := args[key].(string)
	if !ok || raw == "" {
		return nil
	}
	return &raw
}

// parseCivilDate parses a "YYYY-MM-DD" (or RFC3339) string by reusing civildate.Date's own JSON
// decoder, instead of re-deriving date/timezone handling — see that package's doc comment on why a
// bare calendar date isn't just a time.Time.
func parseCivilDate(s string) (civildate.Date, error) {
	var d civildate.Date
	if err := json.Unmarshal([]byte(strconv.Quote(s)), &d); err != nil {
		return civildate.Date{}, fmt.Errorf("assistant: data inválida %q: %w", s, err)
	}
	return d, nil
}

// optionalCivilDate parses args[key] as a date if present and non-empty, nil otherwise.
func optionalCivilDate(args map[string]any, key string) (*civildate.Date, error) {
	raw, ok := args[key].(string)
	if !ok || raw == "" {
		return nil, nil
	}
	d, err := parseCivilDate(raw)
	if err != nil {
		return nil, err
	}
	return &d, nil
}
