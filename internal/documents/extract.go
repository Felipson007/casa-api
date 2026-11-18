package documents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"

	"github.com/atlas/atlas-go/internal/categories"
	"github.com/atlas/atlas-go/internal/categorize"
	"github.com/atlas/atlas-go/internal/platform/money"
)

// classifyOrigin asks the configured Pandora provider for the document's logical destination.
// Native PDF text is preferred; images and scanned PDFs are read by the provider's vision model.
// The closed enum prevents an unexpected model response from leaking into persisted data.
type classifiedDocumentMetadata struct {
	origin         Origin
	summary        *string
	institution    *string
	amount         *string
	expirationDate *time.Time
	externalURL    *string
	notes          *string
}

func (s *Service) classifyOrigin(ctx context.Context, filename string, fileType FileType, content []byte) (classifiedDocumentMetadata, string) {
	text, readErr := s.documentText(ctx, filename, fileType, content)
	if readErr != nil {
		log.Printf("documents.classifyOrigin: document reading failed for %q: %v", filename, readErr)
	}
	fields, err := s.structured(
		ctx,
		"Você classifica documentos pessoais em uma categoria fechada. Domínios de internet, registros de domínio, licenças, certificados e serviços com vigência ou renovação são CONTRATO, nunca FISCAL.",
		fmt.Sprintf("Nome do arquivo: %s\nConteúdo extraído: %s", filename, text),
		`Retorne SOMENTE um JSON: {"origin":"FATURA, HOLERITE, EXAME, PESSOAL, BEM, PET, SEGURO, GARANTIA, FISCAL, CONTRATO ou OUTRO","summary":"resumo curto e objetivo em português","institution":"instituição/empresa/registrador ou vazio","amount":"valor monetário principal ou vazio","expirationDate":"YYYY-MM-DD ou vazio","externalUrl":"URL de gerenciamento ou vazio","notes":"detalhes importantes como tipo e renovação automática ou vazio"}`,
	)
	if err != nil {
		log.Printf("documents.classifyOrigin: AI classification failed for %q: %v", filename, err)
		return classifiedDocumentMetadata{origin: OriginOther}, text
	}
	var origin Origin
	switch strings.ToUpper(strings.TrimSpace(fields["origin"])) {
	case "FATURA":
		origin = OriginInvoice
	case "HOLERITE":
		origin = OriginPaystub
	case "EXAME":
		origin = OriginLabResult
	case "PESSOAL":
		origin = OriginPersonal
	case "BEM":
		origin = OriginAsset
	case "PET":
		origin = OriginPet
	case "SEGURO":
		origin = OriginInsurance
	case "GARANTIA":
		origin = OriginWarranty
	case "FISCAL":
		origin = OriginTax
	case "CONTRATO":
		origin = OriginContract
	default:
		origin = OriginOther
	}
	// Registro de domínio é um contrato com vigência. Esta proteção determinística evita que
	// menções a pagamento/taxa no painel do registrador desviem o documento para Fiscal.
	classificationText := strings.ToLower(filename + " " + text)
	if strings.Contains(classificationText, "registro.br") ||
		strings.Contains(classificationText, "registro de domínio") ||
		strings.Contains(classificationText, "registro de dominio") {
		origin = OriginContract
	}
	var summary *string
	if value := strings.TrimSpace(fields["summary"]); value != "" {
		summary = &value
	}
	metadata := classifiedDocumentMetadata{origin: origin, summary: summary}
	if value := strings.TrimSpace(fields["institution"]); value != "" {
		metadata.institution = &value
	}
	if value, ok := parseAmount(fields["amount"]); ok {
		metadata.amount = &value
	}
	if value, ok := parseDate(fields["expirationDate"]); ok {
		expiration := value.Time()
		metadata.expirationDate = &expiration
	}
	if value := strings.TrimSpace(fields["externalUrl"]); value != "" {
		metadata.externalURL = &value
	}
	if value := strings.TrimSpace(fields["notes"]); value != "" {
		metadata.notes = &value
	}
	return metadata, text
}

const (
	// extractionAttempts / extractionRetryBackoff bound structuredIntoRaw's retry loop — enough
	// to ride out a gateway hiccup without hammering a genuinely unavailable provider.
	extractionAttempts     = 3
	extractionRetryBackoff = 5 * time.Second
	// extractionBudget is the deadline the whole extraction of one document runs under. It also
	// raises the per-call gateway timeout above pandora's interactive-chat default (see
	// pandora.ChatMulti): a full statement's structured extraction takes minutes per chunk.
	extractionBudget = 25 * time.Minute
)

// Extract reads a document's bytes, asks Pandora to structure the relevant fields, and writes the
// result into the matching module (expenses for INVOICE, income for PAYSTUB, health for
// LAB_RESULT). It is intentionally best-effort: any failure is logged and swallowed so the upload
// flow is never blocked.
func (s *Service) Extract(ctx context.Context, doc Document, content []byte) {
	if s.pandora == nil {
		return
	}
	text, err := s.documentText(ctx, doc.OriginalFilename, doc.FileType, content)
	if err != nil {
		log.Printf("documents.Extract: document reading failed for %s: %v", doc.ID, err)
		return
	}
	if strings.TrimSpace(text) == "" {
		log.Printf("documents.Extract: no readable text in %s — skipping", doc.ID)
		return
	}
	s.extractText(ctx, doc, text, content)
}

// extractText structures a document whose text was already read. content (the file's bytes, may
// be nil) lets format-specific readers use the layout the plain text loses (see
// parseItauStatement).
func (s *Service) extractText(ctx context.Context, doc Document, text string, content []byte) {
	// One budget for the whole document. It bounds a wedged extraction AND, because pandora reads
	// the context deadline, lifts each gateway call above the interactive-chat timeout that was
	// killing statement extraction mid-way.
	ctx, cancel := context.WithTimeout(ctx, extractionBudget)
	defer cancel()

	switch doc.Origin {
	case OriginInvoice:
		if s.expenses != nil {
			s.extractInvoice(ctx, doc, text, content)
		}
	case OriginPaystub:
		if s.income != nil {
			s.extractPaystub(ctx, doc, text)
		}
	case OriginLabResult:
		s.extractLabResult(ctx, doc, text)
	default:
		// PERSONAL/ASSET/PET/OTHER: no structured extraction target yet.
	}
}

func (s *Service) documentText(ctx context.Context, filename string, fileType FileType, content []byte) (string, error) {
	if fileType == FileTypePDF {
		text, err := extractPDFText(content)
		if err == nil && strings.TrimSpace(text) != "" {
			return text, nil
		}
		return s.pandora.ReadDocument(ctx, filename, "application/pdf", content)
	}
	contentType, ok := map[FileType]string{
		FileTypePNG: "image/png", FileTypeJPEG: "image/jpeg", FileTypeWEBP: "image/webp",
	}[fileType]
	if !ok {
		return "", fmt.Errorf("unsupported document type %s", fileType)
	}
	return s.pandora.ReadDocument(ctx, filename, contentType, content)
}

// invoiceItem is one line the model pulls from a statement — a purchase, a fee, or a refund
// (Amount is signed: negative for estornos). Date is the real transaction date; it becomes the
// expense's original_date, never its competência.
type invoiceItem struct {
	Amount      string `json:"amount"`
	Date        string `json:"date"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Recurring   bool   `json:"recurring"`
}

// invoiceExtraction is the whole structured reply for one chunk: the line items plus, on the
// chunk that carries the statement header/footer, the printed closing date and total so
// reconciliation has something authoritative to check against.
type invoiceExtraction struct {
	ClosingDate  string        `json:"closingDate"`
	DueDate      string        `json:"dueDate"`
	InvoiceTotal string        `json:"invoiceTotal"`
	Items        []invoiceItem `json:"items"`
}

// invoiceSystemPrompt is deliberately explicit about what counts as a line: the household's real
// invoices only reconciled once fees/charges/refunds were named one by one. A model told merely to
// "extract the purchases" leaves out anuidade, IOF, juros and estornos, and the line sum then
// falls short of the printed total by exactly those.
const invoiceSystemPrompt = "Você é um extrator de dados financeiros rigoroso de FATURAS de cartão e extratos bancários brasileiros. O documento pode ter várias páginas e mais de uma seção de lançamentos (cartão titular, cartões adicionais, lançamentos internacionais, produtos e serviços, encargos). Examine o documento INTEIRO e extraia TODAS as linhas de QUALQUER seção.\n\n" +
	"ENTRAM na lista, com valor POSITIVO:\n" +
	"- todas as compras e cobranças individuais, de todos os cartões (titular e adicionais)\n" +
	"- compras internacionais (converta para reais quando a fatura já trouxer o valor em R$)\n" +
	"- TARIFAS e TAXAS: anuidade (inclusive parcelada), tarifa de avaliação emergencial, tarifa de saque, taxa de serviço, seguros e assistências\n" +
	"- IOF de qualquer natureza: repasse de IOF sobre compras internacionais, IOF de financiamento, IOF de saque\n" +
	"- ENCARGOS: juros rotativo, juros de parcelamento, juros de mora, multa por atraso, encargos de financiamento\n\n" +
	"ENTRAM na lista, com valor NEGATIVO:\n" +
	"- ESTORNOS, DEVOLUÇÕES, CANCELAMENTOS, reembolsos e créditos de qualquer tipo\n" +
	"- descontos, cashback e bonificações abatidos na fatura\n" +
	"- ajustes a crédito (linhas marcadas 'CR', entre parênteses ou com sinal de menos)\n\n" +
	"NÃO entram (são linhas de resumo, não lançamentos): 'total desta fatura', 'total da fatura anterior', 'saldo anterior', 'pagamento efetuado', 'pagamento recebido', 'saldo financiado', 'total dos lançamentos atuais', subtotais por cartão ou por seção, e limites de crédito.\n\n" +
	"ANTES DE RESPONDER, faça a conferência obrigatória: some TODOS os itens da sua lista (compras + tarifas + IOF + encargos − estornos/devoluções/créditos) e compare com o 'Total desta fatura' impresso. Se não bater, você esqueceu alguma coisa — reexamine, nesta ordem: (1) seções de cartões adicionais, (2) lançamentos internacionais, (3) produtos e serviços, (4) repasse de IOF, (5) encargos e juros, (6) estornos. Só responda quando a soma fechar."

const invoiceFormatInstruction = `Retorne SOMENTE um JSON: {"closingDate":"YYYY-MM-DD ou vazio (data de FECHAMENTO da fatura)","dueDate":"YYYY-MM-DD ou vazio (data de VENCIMENTO da fatura — leia com atenção, é a data em que a fatura vence, geralmente impressa como 'vencimento' no topo)","invoiceTotal":"1234.56 ou vazio (Total desta fatura impresso)","items":[{"amount":"1234.56 (NEGATIVO para estorno, devolução, cancelamento, crédito ou desconto)","date":"YYYY-MM-DD (data da transação; se só dia/mês, use o ano da fatura)","description":"estabelecimento/serviço sanitizado em Title Case, ou o nome do encargo (ex.: 'Anuidade Parcela 3 de 12', 'IOF Repasse', 'Juros Rotativo', 'Estorno - Mercado Livre')","category":"Moradia, Alimentação, Transporte, Saúde, Educação, Assinaturas, Compras, Lazer, Pets, Impostos e taxas, Tarifas e IOF, Ajustes e Estornos ou Outros","recurring":false}]}`

// reconcileToleranceBRL is the largest line-sum vs printed-total gap treated as "reconciled" —
// a couple of cents of rounding across a hundred lines, not a missed section.
const reconcileToleranceBRL = "0.02"

// extractInvoice itemizes a whole statement and normalizes every line to the closing-month
// competência (day 1), then stages the result at ExtractionAwaitingReview instead of writing
// straight to expenses — the Inbox (frontend) shows it for review, and only
// Service.ConfirmExtraction (called from POST .../confirm-extraction) turns it into real expense
// rows, once the user has accepted or corrected it. The sum-vs-printed-total check still runs
// here so the review UI can show the "doesn't add up" warning immediately (Delta/Note), but a
// mismatch no longer blocks anything — the user resolves it by editing before confirming.
func (s *Service) extractInvoice(ctx context.Context, doc Document, text string, content []byte) {
	s.beginExtraction(ctx, doc.ID)

	examples, memory := s.expenseCategoryMemory(ctx)

	var (
		ext        invoiceExtraction
		items      []parsedInvoiceItem
		target     string
		extractErr error
	)
	// A layout-aware reader first: it is exact and instant when it recognises the statement, and
	// it only answers when its lines reconcile with the printed total to the cent.
	if st, err := readKnownStatement(doc, content); err == nil {
		ext = invoiceExtraction{
			ClosingDate:  st.closingDate.Format("2006-01-02"),
			DueDate:      st.dueDate.Format("2006-01-02"),
			InvoiceTotal: st.total,
		}
		items = st.invoiceItems() // no dedupe: two identical rides on one day are two charges
		target = st.total
		log.Printf("documents.Extract: invoice %s read by the Itaú statement reader (%d line(s), reconciled)", doc.ID, len(items))
	} else {
		if !errors.Is(err, errNotItau) {
			log.Printf("documents.Extract: Itaú statement reader declined %s: %v — falling back to the model", doc.ID, err)
		}
		instruction := invoiceFormatInstructionFor(examples)
		ext, extractErr = s.extractInvoiceItems(ctx, text, instruction)
		items = dedupeInvoiceItems(parseInvoiceItems(ext.Items))

		// One self-correction pass: if we have a target and the current lines don't reach it, ask the
		// model for the COMPLETE corrected list over the full text, naming what it currently has.
		target = firstNonEmptyAmount(ext.InvoiceTotal, deref(doc.Amount))
		if target != "" && !amountsWithin(sumInvoiceItems(items), target, reconcileToleranceBRL) {
			if retry := s.retryInvoiceItems(ctx, text, items, target, instruction); len(retry) > 0 {
				retryItems := dedupeInvoiceItems(parseInvoiceItems(retry))
				if betterReconciliation(sumInvoiceItems(retryItems), sumInvoiceItems(items), target) {
					items = retryItems
				}
			}
		}
	}

	// The household's own classifications decide (and fill) categories line by line — this is both
	// the consistency fix and the fallback when the model returned no category.
	applyCategoryMemory(items, memory)

	if len(items) == 0 {
		note := extractionFailureNote(extractErr, "A extração automática não encontrou lançamentos nesta fatura. Envie o arquivo novamente ou lance os itens manualmente.")
		s.finishExtraction(ctx, doc, ExtractionResult{
			Status: ExtractionFailed,
			Note:   &note,
		})
		return
	}

	closing := resolveClosingMonth(doc, ext.ClosingDate, ext.DueDate, invoiceItemDates(items))

	draft := FaturaExtraida{
		ClosingDate:  closing.Format("2006-01-02"),
		DueDate:      ext.DueDate,
		InvoiceTotal: target,
		Items:        make([]FaturaExtraidaItem, 0, len(items)),
	}
	for _, it := range items {
		draft.Items = append(draft.Items, FaturaExtraidaItem{
			Amount: it.amount, Date: it.date.Format("2006-01-02"),
			Description: it.description, Category: it.category, Recurring: it.recurring,
			CategorySource: it.categorySource,
		})
	}
	envelope, err := json.Marshal(PendingExtraction{Kind: PendingExtractionKindFatura, Fatura: &draft})
	if err != nil {
		log.Printf("documents.Extract: marshaling invoice draft for %s: %v", doc.ID, err)
		s.finishExtraction(ctx, doc, ExtractionResult{
			Status: ExtractionFailed,
			Note:   strptr("Falha ao preparar a fatura para revisão. Tente reenviar o documento."),
		})
		return
	}

	sum := sumInvoiceItems(items)
	res := reconcileVerdict(sum, target, closing, len(items), "fatura")
	res.Status = ExtractionAwaitingReview
	pending := string(envelope)
	res.PendingExtraction = &pending
	s.finishExtraction(ctx, doc, res)
	log.Printf("documents.Extract: invoice %s → %d line(s) staged for review, competência %s", doc.ID, len(items), closing.Format("2006-01"))
}

// readKnownStatement runs the layout-aware statement readers over a PDF's bytes.
func readKnownStatement(doc Document, content []byte) (itauStatement, error) {
	if doc.FileType != FileTypePDF || len(content) == 0 {
		return itauStatement{}, errNotItau
	}
	return parseItauStatement(content)
}

// extractInvoiceItems asks for the whole statement in ONE call — full text, one complete JSON
// back — which is what lets the model read the header (closing/due date, printed total) and run
// its own sum check against every section it just listed. The loop only ever runs more than once
// for a document too large to fit in a single pass (see statementChunkRunes); nothing here
// truncates, which is the bug that used to lose most of a multi-page invoice's lines.
func (s *Service) extractInvoiceItems(ctx context.Context, text, instruction string) (invoiceExtraction, error) {
	var merged invoiceExtraction
	var lastErr error
	for _, chunk := range chunkText(text, statementChunkRunes, statementChunkOverlapRunes) {
		var part invoiceExtraction
		if err := s.structuredIntoRaw(ctx, invoiceSystemPrompt, chunk, instruction, &part); err != nil {
			log.Printf("documents.Extract: invoice chunk structuring failed: %v", err)
			lastErr = err
			continue
		}
		merged.Items = append(merged.Items, part.Items...)
		if merged.ClosingDate == "" {
			merged.ClosingDate = strings.TrimSpace(part.ClosingDate)
		}
		if merged.DueDate == "" {
			merged.DueDate = strings.TrimSpace(part.DueDate)
		}
		if merged.InvoiceTotal == "" {
			merged.InvoiceTotal = strings.TrimSpace(part.InvoiceTotal)
		}
	}
	return merged, lastErr
}

func (s *Service) retryInvoiceItems(ctx context.Context, text string, have []parsedInvoiceItem, target, instruction string) []invoiceItem {
	var lines strings.Builder
	for _, it := range have {
		fmt.Fprintf(&lines, "- %s | %s | %s\n", it.amount, it.date.Format("2006-01-02"), it.description)
	}
	prompt := fmt.Sprintf(
		"O total impresso desta fatura é R$ %s, mas os lançamentos que extraí somam R$ %s. Faltam linhas — provavelmente uma seção inteira (cartão adicional, lançamentos internacionais, produtos e serviços, repasse de IOF). Reexamine o documento inteiro e devolva a LISTA COMPLETA E CORRIGIDA de todos os lançamentos.\n\nLançamentos que já tenho:\n%s",
		target, sumInvoiceItems(have), lines.String(),
	)
	var out invoiceExtraction
	if err := s.structuredIntoRaw(ctx, invoiceSystemPrompt, text+"\n\n"+prompt, instruction, &out); err != nil {
		log.Printf("documents.Extract: invoice retry structuring failed: %v", err)
		return nil
	}
	return out.Items
}

// maxPromptCategoryExamples bounds how many past classifications are injected into the invoice
// prompt — enough to pin down the household's recurring merchants without bloating the request.
const maxPromptCategoryExamples = 120

// expenseCategoryMemory loads the household's classified expenses and builds the category memory.
// A failure is non-fatal: extraction proceeds and the per-line categories fall back to the model's.
func (s *Service) expenseCategoryMemory(ctx context.Context) ([]categorize.Example, categorize.Memory) {
	if s.expenses == nil {
		return nil, categorize.NewMemory(nil)
	}
	examples, err := s.expenses.CategoryHistory(ctx)
	if err != nil {
		log.Printf("documents.Extract: category history lookup failed: %v", err)
		return nil, categorize.NewMemory(nil)
	}
	return examples, categorize.NewMemory(examples)
}

// invoiceFormatInstructionFor appends the household's real classifications to the base instruction,
// so the model reuses the same category for a merchant it has seen before instead of inventing a
// variation each statement. Falls back to the base instruction when there is nothing to add.
func invoiceFormatInstructionFor(examples []categorize.Example) string {
	if len(examples) == 0 {
		return invoiceFormatInstruction
	}
	seen := map[string]struct{}{}
	var lines strings.Builder
	count := 0
	for _, example := range examples {
		desc := truncate(strings.TrimSpace(example.Description), 40)
		category := strings.TrimSpace(example.Category)
		if desc == "" || category == "" {
			continue
		}
		key := strings.ToLower(desc) + "|" + strings.ToLower(category)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		fmt.Fprintf(&lines, "- %s → %s\n", desc, category)
		count++
		if count >= maxPromptCategoryExamples {
			break
		}
	}
	if count == 0 {
		return invoiceFormatInstruction
	}
	return invoiceFormatInstruction + "\n\nO usuário já classificou estes estabelecimentos/encargos assim; use a MESMA categoria quando reconhecer o mesmo estabelecimento (não invente variações):\n" + lines.String()
}

// applyCategoryMemory reconciles each extracted line with the household's own history: a strong
// match (same merchant) wins over the model's guess so one merchant keeps one category across
// statements. A weak, low-confidence match is not applied — the line stays uncategorised (NONE)
// when the model gave nothing, so it surfaces in the manual-review modal.
func applyCategoryMemory(items []parsedInvoiceItem, memory categorize.Memory) {
	for i := range items {
		suggestion, ok := memory.Suggest(items[i].description)
		hasCategory := strings.TrimSpace(items[i].category) != ""
		switch {
		case ok && suggestion.Strong:
			items[i].category = suggestion.Category
			items[i].categorySource = "HISTORY"
		case hasCategory:
			items[i].categorySource = "AI"
		default:
			items[i].categorySource = "NONE"
		}
	}
}

func (s *Service) expenseCategoryIDs(ctx context.Context) map[string]*uuid.UUID {
	result := map[string]*uuid.UUID{}
	if s.categories == nil {
		return result
	}
	t := categories.TypeExpense
	items, err := s.categories.List(ctx, &t)
	if err != nil {
		log.Printf("documents.Extract: category lookup failed: %v", err)
		return result
	}
	for _, item := range items {
		id := item.ID
		result[normaliseCategory(item.Name)] = &id
	}
	for _, name := range []string{
		"Moradia", "Alimentação", "Transporte", "Saúde", "Educação", "Assinaturas",
		"Compras", "Lazer", "Pets", "Impostos e taxas", "Tarifas e IOF", "Ajustes e Estornos", "Outros",
	} {
		key := normaliseCategory(name)
		if result[key] != nil {
			continue
		}
		created, createErr := s.categories.Create(ctx, categories.CreateCategoryRequest{Name: name, Type: &t})
		if createErr != nil {
			log.Printf("documents.Extract: category create failed for %q: %v", name, createErr)
			continue
		}
		id := created.ID
		result[key] = &id
	}
	return result
}

func normaliseCategory(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// paystubLine is one row of a holerite — a provento (positive) or a desconto (negative). Kind is
// the model's own label, kept only for the line description; the sign on Amount is authoritative.
type paystubLine struct {
	Amount      string `json:"amount"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
}

// TotalProventos/TotalDescontos are the two printed subtotals, checked SEPARATELY against the
// extracted lines. Checking only the net would let two mistakes cancel — a base de cálculo picked
// up as an earning while a real deduction was missed nets out to the right líquido with both
// sides wrong. Comparing each side catches which way it went, and the earnings side overshooting
// is the specific signature of a base de cálculo having crept in.
type paystubExtraction struct {
	ReferenceMonth string        `json:"referenceMonth"`
	PayDate        string        `json:"payDate"`
	TotalProventos string        `json:"totalProventos"`
	TotalDescontos string        `json:"totalDescontos"`
	NetPay         string        `json:"netPay"`
	Lines          []paystubLine `json:"lines"`
}

// The "bases de cálculo" exclusion is the load-bearing part of this prompt. A holerite's footer
// carries Base INSS / Base FGTS / Base IRRF / FGTS do Mês — large numbers that are inputs to a
// calculation, not money paid or deducted. Treating one as a rubrica inflates the month by
// thousands. It is genuinely confusable: "Salário Base" IS a provento, so a rule like "skip lines
// containing 'base'" would be wrong in the other direction. Hence the explicit both-ways framing.
const paystubSystemPrompt = "Você extrai HOLERITES/contracheques brasileiros em JSON. Extraia SOMENTE as rubricas da tabela de lançamentos — as linhas que têm um código/descrição e um valor nas colunas de PROVENTOS (vencimentos) ou DESCONTOS. Nada fora dessa tabela é lançamento.\n\n" +
	"PROVENTOS (valor POSITIVO): salário base, comissão de cargo, gratificações, horas extras (todas as faixas), adicional noturno/periculosidade/insalubridade, DSR, férias e 1/3 de férias, médias de férias, 13º salário, PLR/bônus, ajuda de custo, auxílios e reembolsos pagos em folha.\n\n" +
	"DESCONTOS (valor NEGATIVO): INSS, Imposto de Renda (IRRF), contribuição sindical, plano de saúde e odontológico, coparticipação, previdência privada (PGBL/VGBL), vale-transporte, vale-refeição, adiantamentos (inclusive adiantamento líquido de férias), empréstimos consignados, pensão alimentícia, faltas e atrasos.\n\n" +
	"NUNCA inclua BASES DE CÁLCULO nem TOTAIS. Elas ficam no RODAPÉ/resumo, fora da tabela de lançamentos, e são valores usados para calcular impostos — não são dinheiro pago nem descontado. Nunca entram na lista: 'Base INSS', 'Base FGTS', 'Base IRRF', 'Base de Cálculo', 'Salário Contribuição', 'FGTS do Mês', 'Faixa IRRF', 'Total de Proventos', 'Total de Vencimentos', 'Total de Descontos', 'Líquido a Receber', 'Valor Líquido', 'Salário Contratual', 'Salário Nominal'.\n\n" +
	"ATENÇÃO — não confunda: 'SALÁRIO BASE' é um PROVENTO real e ENTRA na lista (é a remuneração paga). Já 'BASE INSS', 'BASE FGTS', 'BASE IRRF' e 'BASE DE CÁLCULO' são bases de cálculo e NÃO entram. A diferença não é a palavra 'base': é a posição no documento — se está na tabela de lançamentos com provento/desconto, entra; se está no rodapé de resumo, não entra.\n\n" +
	"ANTES DE RESPONDER, confira as duas somas separadamente: (1) a soma dos seus proventos tem que bater com o 'Total de Proventos' impresso; (2) a soma dos seus descontos tem que bater com o 'Total de Descontos' impresso; (3) proventos menos descontos tem que dar o LÍQUIDO impresso. Se os proventos derem MAIOR que o total impresso, você incluiu uma base de cálculo ou um total por engano — remova. Se derem MENOR, faltou uma rubrica — reexamine."

const paystubFormatInstruction = `Retorne SOMENTE um JSON: {"referenceMonth":"YYYY-MM (competência)","payDate":"YYYY-MM-DD ou vazio","totalProventos":"12493.98 (Total de Proventos impresso)","totalDescontos":"3840.87 (Total de Descontos impresso, positivo)","netPay":"8653.11 (líquido/valor a receber impresso)","lines":[{"amount":"1234.56 (NEGATIVO para desconto)","description":"descrição da rubrica","kind":"PROVENTO ou DESCONTO"}]}`

// extractPaystub itemizes a holerite into its provento/desconto lines, normalizes them all to the
// reference-month competência (day 1), and checks the sum against the printed net pay — the same
// treatment invoices get, so month 08 lands broken into lines like months 01–07 instead of as one
// lump "Pagamento mensal".
func (s *Service) extractPaystub(ctx context.Context, doc Document, text string) {
	s.beginExtraction(ctx, doc.ID)

	var ext paystubExtraction
	var extractErr error
	for _, chunk := range chunkText(text, statementChunkRunes, statementChunkOverlapRunes) {
		var part paystubExtraction
		if err := s.structuredIntoRaw(ctx, paystubSystemPrompt, chunk, paystubFormatInstruction, &part); err != nil {
			log.Printf("documents.Extract: paystub chunk structuring failed: %v", err)
			extractErr = err
			continue
		}
		ext.Lines = append(ext.Lines, part.Lines...)
		if ext.ReferenceMonth == "" {
			ext.ReferenceMonth = strings.TrimSpace(part.ReferenceMonth)
		}
		if ext.PayDate == "" {
			ext.PayDate = strings.TrimSpace(part.PayDate)
		}
		if ext.NetPay == "" {
			ext.NetPay = strings.TrimSpace(part.NetPay)
		}
		if ext.TotalProventos == "" {
			ext.TotalProventos = strings.TrimSpace(part.TotalProventos)
		}
		if ext.TotalDescontos == "" {
			ext.TotalDescontos = strings.TrimSpace(part.TotalDescontos)
		}
	}

	type parsedLine struct {
		amount string
		desc   string
	}
	var lines []parsedLine
	seen := map[string]struct{}{}
	for _, l := range ext.Lines {
		amt, ok := parseSignedAmount(l.Amount)
		desc := strings.TrimSpace(l.Description)
		if !ok || desc == "" {
			continue
		}
		key := amt + "|" + strings.ToLower(desc)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		lines = append(lines, parsedLine{amount: amt, desc: desc})
	}
	if len(lines) == 0 {
		note := extractionFailureNote(extractErr, "A extração automática não conseguiu ler as rubricas deste holerite. Envie o arquivo novamente ou lance manualmente.")
		s.finishExtraction(ctx, doc, ExtractionResult{
			Status: ExtractionFailed,
			Note:   &note,
		})
		return
	}

	closing := resolveReferenceMonth(doc, ext.ReferenceMonth, ext.PayDate)

	sum, earnings, deductions := money.Zero, money.Zero, money.Zero
	draft := HoleriteExtraido{
		ReferenceMonth: closing.Format("2006-01"),
		PayDate:        ext.PayDate,
		TotalProventos: ext.TotalProventos,
		TotalDescontos: ext.TotalDescontos,
		NetPay:         firstNonEmptyAmount(ext.NetPay, deref(doc.Amount)),
		Lines:          make([]HoleriteExtraidoLinha, 0, len(lines)),
	}
	for _, l := range lines {
		sum, _ = money.Add(sum, l.amount)
		kind := "PROVENTO"
		if sign, err := money.Sign(l.amount); err == nil && sign < 0 {
			deductions, _ = money.Add(deductions, l.amount)
			kind = "DESCONTO"
		} else {
			earnings, _ = money.Add(earnings, l.amount)
		}
		draft.Lines = append(draft.Lines, HoleriteExtraidoLinha{Amount: l.amount, Description: l.desc, Kind: kind})
	}
	envelope, err := json.Marshal(PendingExtraction{Kind: PendingExtractionKindHolerite, Holerite: &draft})
	if err != nil {
		log.Printf("documents.Extract: marshaling paystub draft for %s: %v", doc.ID, err)
		s.finishExtraction(ctx, doc, ExtractionResult{
			Status: ExtractionFailed,
			Note:   strptr("Falha ao preparar o holerite para revisão. Tente reenviar o documento."),
		})
		return
	}

	target := draft.NetPay
	res := reconcileVerdict(sum, target, closing, len(lines), "holerite")
	res.Status = ExtractionAwaitingReview
	// A base de cálculo mistaken for an earning is invisible in the net when a deduction is also
	// missed — the two cancel. Checking the earnings side against its own printed subtotal names
	// that failure directly, and overshooting is its signature, so say so rather than making the
	// user work it out from a generic "não fechou". Still only advisory now — Note surfaces in the
	// review dialog, it doesn't block confirming.
	if over := earningsOvershoot(earnings, ext.TotalProventos); over != "" {
		res.Note = strptr(fmt.Sprintf(
			"Os proventos extraídos somam R$ %s, acima do 'Total de Proventos' impresso (R$ %s) — a extração provavelmente incluiu uma BASE DE CÁLCULO (base INSS/FGTS/IRRF) como se fosse rubrica. Confira antes de confirmar.",
			earnings, ext.TotalProventos,
		))
	}
	pending := string(envelope)
	res.PendingExtraction = &pending
	s.finishExtraction(ctx, doc, res)
	log.Printf("documents.Extract: paystub %s → %d line(s) staged for review (proventos %s / descontos %s), competência %s",
		doc.ID, len(lines), earnings, deductions, closing.Format("2006-01"))
}

// earningsOvershoot returns the extracted earnings total when it exceeds the statement's printed
// "Total de Proventos" beyond tolerance, or "" when there's nothing to flag (no printed subtotal,
// unparseable, or the sum is at/below it — a shortfall is the ordinary missing-line case that
// reconcileVerdict already reports).
func earningsOvershoot(earnings, printedTotal string) string {
	if strings.TrimSpace(printedTotal) == "" {
		return ""
	}
	printed, ok := parseSignedAmount(printedTotal)
	if !ok {
		return ""
	}
	diff, err := money.Sub(earnings, printed)
	if err != nil {
		return ""
	}
	if sign, err := money.Sign(diff); err != nil || sign <= 0 {
		return ""
	}
	if amountsWithin(earnings, printed, reconcileToleranceBRL) {
		return ""
	}
	return earnings
}

// examResultRaw is the model's raw JSON reply for a LAB_RESULT document — the typed target
// structuredInto decodes into, before date/assessment/marker normalization builds the ExameExtraido
// draft that actually gets staged.
type examResultRaw struct {
	Date       string                  `json:"date"`
	Assessment string                  `json:"assessment"`
	Organ      string                  `json:"organ"`
	Notes      string                  `json:"notes"`
	Markers    []ExameExtraidoMarcador `json:"markers"`
}

// parseExameMarkers keeps only markers with a name and a numeric value, normalizing Valor/
// ReferenciaMin/ReferenciaMax to the "1234.56" form every other money-shaped field in this codebase
// uses — a marker the model named but couldn't read a number for is worse than useless staged (the
// reviewer can't confirm it), so it's dropped rather than shown blank.
func parseExameMarkers(raw []ExameExtraidoMarcador) []ExameExtraidoMarcador {
	markers := make([]ExameExtraidoMarcador, 0, len(raw))
	for _, m := range raw {
		nome := strings.TrimSpace(m.Nome)
		valor, ok := parseAmount(m.Valor)
		if nome == "" || !ok {
			continue
		}
		marker := ExameExtraidoMarcador{Nome: nome, Valor: valor, Unidade: strings.TrimSpace(m.Unidade)}
		if min, ok := parseAmount(m.ReferenciaMin); ok {
			marker.ReferenciaMin = min
		}
		if max, ok := parseAmount(m.ReferenciaMax); ok {
			marker.ReferenciaMax = max
		}
		markers = append(markers, marker)
	}
	return markers
}

func (s *Service) extractLabResult(ctx context.Context, doc Document, text string) {
	if s.health == nil {
		log.Printf("documents.Extract: no health service configured, skipping lab result for %s", doc.ID)
		return
	}
	if doc.PersonID == nil {
		// Silently returning here used to leave extraction_status untouched (nil forever) — the
		// document never got a PENDING/FAILED row, so it never surfaced in the Inbox and looked
		// like the upload had simply vanished (production, 2026-09-16). Staging it as FAILED with
		// an actionable note puts it in front of the user the same way an unreadable exam does.
		log.Printf("documents.Extract: lab result missing person for %s", doc.ID)
		s.beginExtraction(ctx, doc.ID)
		note := "Este exame não tem uma pessoa vinculada — não é possível gerar o registro de saúde sem isso. Edite o documento, informe a pessoa e use Reextrair."
		s.finishExtraction(ctx, doc, ExtractionResult{Status: ExtractionFailed, Note: &note})
		return
	}
	existing, err := s.health.ListRecords(ctx, doc.PersonID, nil)
	if err != nil {
		log.Printf("documents.Extract: existing lab result lookup failed for %s: %v", doc.ID, err)
		return
	}
	for _, record := range existing {
		if record.DocumentID != nil && *record.DocumentID == doc.ID {
			log.Printf("documents.Extract: lab result already extracted for %s", doc.ID)
			return
		}
	}
	s.beginExtraction(ctx, doc.ID)

	// structuredIntoRaw, not structuredInto: this call was landing with zero retries, unlike
	// invoice/paystub extraction, which already retries transient gateway failures (429/503) 3x
	// with backoff. A real exam upload hit a 503 "model overloaded" on the single attempt and
	// staged as FAILED with a note that wrongly implied the file itself was unreadable (production,
	// 2026-09-17) — the same transient error invoice/paystub extraction shrugs off via retry.
	var raw examResultRaw
	err = s.structuredIntoRaw(ctx,
		"Você extrai dados de EXAMES/LAUDOS LABORATORIAIS em JSON.",
		truncate(text, 12000),
		`Retorne SOMENTE um JSON: {"date":"YYYY-MM-DD","assessment":"NORMAL, ATTENTION ou ALTERED conforme os resultados","organ":"órgão/sistema principal avaliado, se houver um só","notes":"resumo dos principais achados e valores, em português","markers":[{"nome":"nome do exame/marcador exatamente como impresso, ex.: Colesterol Total, Glicemia de Jejum, Hemoglobina","valor":"resultado numérico, ex.: 185.00 (troque vírgula por ponto)","unidade":"unidade impressa, ex.: mg/dL, ou vazio","referenciaMin":"limite inferior do valor de referência impresso, ou vazio","referenciaMax":"limite superior do valor de referência impresso, ou vazio"}]} — markers é a lista de TODOS os resultados individuais do painel (um item por linha do laudo com nome+valor); vazio só se o documento não trouxer nenhum resultado numérico tabulado.`,
		&raw,
	)
	if err != nil {
		log.Printf("documents.Extract: lab result structuring failed: %v", err)
		note := extractionFailureNote(err, "A extração automática não conseguiu ler este exame. Envie o arquivo novamente ou preencha manualmente.")
		s.finishExtraction(ctx, doc, ExtractionResult{Status: ExtractionFailed, Note: &note})
		return
	}
	date, ok := parseDate(raw.Date)
	if !ok {
		log.Printf("documents.Extract: lab result missing date for %s", doc.ID)
		note := "A extração automática não conseguiu identificar a data deste exame. Envie o arquivo novamente ou preencha manualmente."
		s.finishExtraction(ctx, doc, ExtractionResult{Status: ExtractionFailed, Note: &note})
		return
	}
	// parseAssessmentStatus/date round-trip through their canonical form (not the model's raw
	// string) so the review dialog and ConfirmExtraction see the same normalized values. A
	// nil/unrecognised assessment stages as "" — ConfirmExtraction re-validates it and rejects an
	// empty/invalid value rather than silently defaulting, so the reviewer has to pick one.
	var assessment string
	if status := parseAssessmentStatus(raw.Assessment); status != nil {
		assessment = string(*status)
	}
	draft := ExameExtraido{
		Date:       date.Time().Format("2006-01-02"),
		Assessment: assessment,
		Organ:      strings.TrimSpace(raw.Organ),
		Notes:      raw.Notes,
		Markers:    parseExameMarkers(raw.Markers),
	}
	envelope, err := json.Marshal(PendingExtraction{Kind: PendingExtractionKindExame, Exame: &draft})
	if err != nil {
		log.Printf("documents.Extract: marshaling lab result draft for %s: %v", doc.ID, err)
		note := "Falha ao preparar o exame para revisão. Tente reenviar o documento."
		s.finishExtraction(ctx, doc, ExtractionResult{Status: ExtractionFailed, Note: &note})
		return
	}
	pending := string(envelope)
	s.finishExtraction(ctx, doc, ExtractionResult{Status: ExtractionAwaitingReview, PendingExtraction: &pending})
	log.Printf("documents.Extract: lab result %s staged for review", doc.ID)
}

// structured calls Pandora with the document text and a JSON-only instruction, then parses the
// first JSON object found in the reply.
func (s *Service) structured(ctx context.Context, system, text, formatInstruction string) (map[string]string, error) {
	user := fmt.Sprintf("%s\n\n---\n%s", formatInstruction, truncate(text, 12000))
	out, err := s.pandora.Chat(ctx, s.pandora.ExtractionModel(ctx), system, user)
	if err != nil {
		return nil, err
	}
	return parseFirstJSON(out)
}

// structuredIntoRaw is structured() decoding into a typed target instead of a flat
// map[string]string, and WITHOUT the 12k-rune truncation baked in — invoice/paystub extraction has
// already split the document into model-sized chunks, so truncating here would silently drop the
// tail of every chunk and, with it, whole sections of a statement; lab result extraction (the only
// other caller) truncates itself before calling in, same 12k cap as structured().
//
// Retries on failure: one gateway timeout or one unparseable reply used to abandon the whole
// statement (a real production failure — a paystub came back FAILED with zero lines because a
// single completion call exceeded the client timeout). These calls are idempotent reads, so
// retrying is safe; only the last error surfaces.
func (s *Service) structuredIntoRaw(ctx context.Context, system, text, formatInstruction string, target any) error {
	user := fmt.Sprintf("%s\n\n---\n%s", formatInstruction, text)
	var lastErr error
	for attempt := 1; attempt <= extractionAttempts; attempt++ {
		out, err := s.pandora.Chat(ctx, s.pandora.ExtractionModel(ctx), system, user)
		if err == nil {
			if err = parseFirstJSONInto(out, target); err == nil {
				return nil
			}
		}
		lastErr = err
		if attempt < extractionAttempts {
			log.Printf("documents.Extract: attempt %d/%d failed (%v) — retrying", attempt, extractionAttempts, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * extractionRetryBackoff):
			}
		}
	}
	return lastErr
}

// extractPDFText pulls the text layer out of a PDF using ledongthuc/pdf. Returns "" for image-only
// PDFs (the Text slice is empty).
//
// ledongthuc/pdf panics on some malformed/truncated PDFs instead of returning an error (observed in
// production: "panic: malformed PDF: reading at offset 18056: unexpected EOF"), which — uncaught —
// took down the whole backend process since this runs off the request goroutine (see safeGo in
// extract_helpers.go). The recover() below converts that into a normal error so one bad upload
// degrades to an ExtractionFailed document instead of a process crash.
func extractPDFText(content []byte) (text string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("pdf parse: recovered from panic: %v", rec)
		}
	}()

	reader, err := pdf.NewReader(bytesReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("pdf parse: %w", err)
	}
	// Reader.GetPlainText (not a manual Content().Text walk) is what reconstructs real word
	// spacing from the content stream's individual glyph-showing operations — a PDF commonly
	// emits one operation per character, and joining those with a single space each produced
	// "T E S T E   1 2 3" instead of "TESTE 123" (caught by
	// TestExtractPDFTextReadsATextLayer, which generates a real digital PDF rather than only
	// exercising the malformed-input path below).
	plain, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("pdf parse: %w", err)
	}
	body, err := io.ReadAll(plain)
	if err != nil {
		return "", fmt.Errorf("pdf parse: read plain text: %w", err)
	}
	return string(body), nil
}
