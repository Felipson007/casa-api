package documents

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/cardspend"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/atlas/atlas-go/internal/platform/money"
)

// This file holds the shared machinery behind extract.go's itemized INVOICE/PAYSTUB extraction:
// splitting a whole statement into model-sized chunks, resolving the competência (closing/
// reference month), scoring how well the extracted lines reconcile against the printed total, and
// the Reconcile entrypoint the Inbox calls when it can't reconcile on its own and the user has
// supplied the correct number.

const (
	// statementChunkRunes bounds one model call's slice of the statement text — and is set high
	// enough that a real statement is a SINGLE call seeing the whole document, which is the point.
	//
	// The model reasons far better over a complete statement than over slices of one: only a pass
	// that contains the header can read the closing/due date and the printed total, and only a
	// pass that contains every section can run the "sum it all and check against the total"
	// self-check the prompt asks for. Splitting also multiplies the number of gateway calls, and
	// each one is a chance to time out (a paystub came back FAILED in production for exactly
	// that). So chunking is a safety valve for a pathologically large document, not the norm —
	// chunkText returns the text untouched, as one chunk, whenever it fits under this ceiling.
	//
	// The old code capped extraction input at 12k runes, which is why a multi-page invoice lost
	// most of its lines; this is deliberately far above any statement this household uploads
	// (a 174-line Itaú invoice's text layer runs ~20–40k) while staying within a normal model
	// context window.
	statementChunkRunes = 60000
	// statementChunkOverlapRunes re-feeds the tail of the previous chunk so a line split across
	// the boundary still lands whole in at least one pass (dedupe removes the repeat). Only
	// relevant when a document actually exceeds statementChunkRunes.
	statementChunkOverlapRunes = 2000
)

// adjustmentDescriptionInvoice / adjustmentDescriptionPaystub label the single reconciliation
// line Reconcile adds for whatever the automatic extraction still missed after the user confirmed
// the real total — matched by prefix on a re-Reconcile so it's replaced, never stacked.
const (
	adjustmentDescriptionInvoice = "Ajuste de fechamento da fatura"
	adjustmentDescriptionPaystub = "Ajuste de holerite"
	adjustmentCategoryName       = "Ajustes e Estornos"
)

// chunkText splits text into overlapping windows of at most maxRunes runes. Returns the text
// unchanged (one element) when it already fits.
func chunkText(text string, maxRunes, overlapRunes int) []string {
	r := []rune(text)
	if len(r) <= maxRunes {
		return []string{text}
	}
	step := maxRunes - overlapRunes
	if step <= 0 {
		step = maxRunes
	}
	var chunks []string
	for start := 0; start < len(r); start += step {
		end := start + maxRunes
		if end >= len(r) {
			chunks = append(chunks, string(r[start:]))
			break
		}
		chunks = append(chunks, string(r[start:end]))
	}
	return chunks
}

// ---------------------------------------------------------------------------------------------
// Invoice line parsing / merging
// ---------------------------------------------------------------------------------------------

type parsedInvoiceItem struct {
	amount         string // signed numeric(15,2)-as-string; negative for estornos
	date           time.Time
	description    string
	category       string
	categorySource string // AI | HISTORY — see applyCategoryMemory
	recurring      bool
}

func parseInvoiceItems(items []invoiceItem) []parsedInvoiceItem {
	out := make([]parsedInvoiceItem, 0, len(items))
	for _, it := range items {
		amt, amtOK := parseSignedAmount(it.Amount)
		date, dateOK := parseDate(it.Date)
		desc := strings.TrimSpace(it.Description)
		if !amtOK || !dateOK || desc == "" {
			continue
		}
		out = append(out, parsedInvoiceItem{
			amount:      amt,
			date:        date.Time(),
			description: desc,
			category:    strings.TrimSpace(it.Category),
			recurring:   it.Recurring,
		})
	}
	return out
}

// dedupeInvoiceItems drops repeats introduced by the chunk overlap (same amount, same day, same
// normalized description). Genuine identical same-day charges collapse too — the same tradeoff
// the old fingerprint dedupe made.
func dedupeInvoiceItems(items []parsedInvoiceItem) []parsedInvoiceItem {
	seen := make(map[string]struct{}, len(items))
	out := make([]parsedInvoiceItem, 0, len(items))
	for _, it := range items {
		key := strings.Join([]string{
			it.amount,
			it.date.Format("2006-01-02"),
			strings.ToLower(strings.Join(strings.Fields(it.description), " ")),
		}, "|")
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, it)
	}
	return out
}

func sumInvoiceItems(items []parsedInvoiceItem) string {
	total := money.Zero
	for _, it := range items {
		if next, err := money.Add(total, it.amount); err == nil {
			total = next
		}
	}
	return total
}

func invoiceItemDates(items []parsedInvoiceItem) []time.Time {
	dates := make([]time.Time, 0, len(items))
	for _, it := range items {
		dates = append(dates, it.date)
	}
	return dates
}

// ---------------------------------------------------------------------------------------------
// Amount / pointer helpers
// ---------------------------------------------------------------------------------------------

// parseSignedAmount is parseAmount plus sign handling — parseAmount strips every non-digit,
// non-dot character, so "-106,60", "(106,60)" and "106,60-" would all come back positive.
func parseSignedAmount(s string) (string, bool) {
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
		neg = true
	}
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg = true
	}
	if strings.Contains(strings.ToUpper(s), "CR") { // some statements mark credits "CR"
		neg = true
	}
	v, ok := parseAmount(s)
	if !ok {
		return "", false
	}
	if neg && v != "0" && v != "0.00" {
		v = "-" + v
	}
	return v, true
}

func firstNonEmptyAmount(candidates ...string) string {
	for _, c := range candidates {
		if v, ok := parseSignedAmount(c); ok && v != "" {
			return v
		}
	}
	return ""
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func strptr(s string) *string { return &s }

// amountsWithin reports whether |a-b| <= tol.
func amountsWithin(a, b, tol string) bool {
	diff, err := money.Sub(a, b)
	if err != nil {
		return false
	}
	abs := strings.TrimPrefix(diff, "-")
	cmp, err := money.Compare(abs, tol)
	if err != nil {
		return false
	}
	return cmp <= 0
}

// betterReconciliation reports whether candidateSum lands closer to target than currentSum does.
func betterReconciliation(candidateSum, currentSum, target string) bool {
	cand, err1 := money.Sub(candidateSum, target)
	cur, err2 := money.Sub(currentSum, target)
	if err1 != nil || err2 != nil {
		return false
	}
	cmp, err := money.Compare(strings.TrimPrefix(cand, "-"), strings.TrimPrefix(cur, "-"))
	return err == nil && cmp < 0
}

// ---------------------------------------------------------------------------------------------
// Competência resolution
// ---------------------------------------------------------------------------------------------

// resolveClosingMonth decides which month every line of an invoice belongs to — the competência,
// normalized to day 1.
//
// The competência is the month the invoice is BILLED IN (its vencimento's month), not the month
// its purchases happened nor the month it closed. That's this household's established convention,
// visible in the data the ETL brought over from the Java app and mirrored by the frontend's
// competenciaDoVencimento: invoice "02" has document_date 2026-02-08 (vencimento), covers
// purchases from 01/01–31/01, and sits in competência 2026-02. A card that closes ~day 25 and
// falls due ~day 8 therefore always lands one month AFTER its closing date — which is why the
// due date, not the closing date, leads this list. Getting that backwards stacks a new invoice on
// top of the previous month's (verified in production: an invoice due 2026-09-08 landed in
// 2026-08 alongside invoice 08 and doubled that month's total).
//
// Priority order:
//  1. the due date (vencimento) the model read off the statement — the convention itself
//  2. DocumentDate — historically holds the vencimento for every imported invoice
//  3. ExpirationDate — auto-classification files an invoice's vencimento here
//  4. the closing date the model read, PLUS ONE MONTH (a statement closes the month before it's
//     due for this household's cards; see above)
//  5. last resort: the most common (purchase month + 1) across the line items
func resolveClosingMonth(doc Document, closingDate, dueDate string, itemDates []time.Time) time.Time {
	if t, ok := parseMonthStart(dueDate); ok {
		return t
	}
	if doc.DocumentDate != nil {
		return firstOfMonthUTC(*doc.DocumentDate)
	}
	if doc.ExpirationDate != nil {
		return firstOfMonthUTC(*doc.ExpirationDate)
	}
	if t, ok := parseMonthStart(closingDate); ok {
		return t.AddDate(0, 1, 0)
	}
	if t, ok := mostCommonMonthStart(itemDates, 1); ok {
		return t
	}
	return firstOfMonthUTC(time.Now())
}

// resolveReferenceMonth is resolveClosingMonth for a paystub: the reference/competência month the
// model read, else DocumentDate, else the pay date's month, else the pay date shifted nowhere
// (paystubs are dated in the month they refer to), else now.
func resolveReferenceMonth(doc Document, referenceMonth, payDate string) time.Time {
	if t, ok := parseMonthStart(referenceMonth); ok {
		return t
	}
	if doc.DocumentDate != nil {
		return firstOfMonthUTC(*doc.DocumentDate)
	}
	if t, ok := parseMonthStart(payDate); ok {
		return t
	}
	return firstOfMonthUTC(time.Now())
}

// ---------------------------------------------------------------------------------------------
// Reconciliation verdict
// ---------------------------------------------------------------------------------------------

// reconcileVerdict turns a line sum + a (possibly empty) printed target into the ExtractionResult
// that gets persisted on the document. No target → nothing to check, RECONCILED. Within tolerance
// → RECONCILED. Otherwise → NEEDS_INPUT with a human-readable question for the Inbox.
func reconcileVerdict(sum, target string, closing time.Time, lineCount int, kind string) ExtractionResult {
	res := ExtractionResult{
		StatementClosingDate: timePtr(closing),
	}
	if target == "" {
		res.Status = ExtractionReconciled
		return res
	}
	res.StatementTotal = strptr(target)
	delta, err := money.Sub(sum, target)
	if err != nil {
		res.Status = ExtractionReconciled
		return res
	}
	res.Delta = strptr(delta)
	if amountsWithin(sum, target, reconcileToleranceBRL) {
		res.Status = ExtractionReconciled
		return res
	}
	res.Status = ExtractionNeedsInput
	res.Note = strptr(fmt.Sprintf(
		"A extração automática somou R$ %s em %d lançamentos, mas o total impresso desta %s é R$ %s (diferença de R$ %s). Confirme o valor correto para o Atlas ajustar o fechamento.",
		strings.TrimPrefix(sum, "-"), lineCount, kind, target, strings.TrimPrefix(delta, "-"),
	))
	return res
}

func timePtr(t time.Time) *time.Time { return &t }

// ---------------------------------------------------------------------------------------------
// Extraction status persistence
// ---------------------------------------------------------------------------------------------

func (s *Service) beginExtraction(ctx context.Context, id uuid.UUID) {
	if err := s.repo.UpdateExtraction(ctx, id, ExtractionResult{Status: ExtractionPending}); err != nil {
		log.Printf("documents: marking extraction pending for %s: %v", id, err)
	}
}

func (s *Service) finishExtraction(ctx context.Context, doc Document, res ExtractionResult) {
	if err := s.repo.UpdateExtraction(ctx, doc.ID, res); err != nil {
		log.Printf("documents: persisting extraction result for %s: %v", doc.ID, err)
	}
}

// ---------------------------------------------------------------------------------------------
// Reconcile — the Inbox answering "qual o valor correto?"
// ---------------------------------------------------------------------------------------------

// Reextract re-runs auto-extraction for an already-uploaded document, replacing whatever it
// produced before. Without it a document whose extraction failed (a gateway timeout mid-run, say)
// is stuck at FAILED forever — the only recovery would be deleting and re-uploading the file,
// losing its classification and links. Runs in the background like the upload path does, so the
// caller gets an immediate answer rather than blocking for minutes; poll the document's
// extractionStatus for the outcome.
func (s *Service) Reextract(ctx context.Context, id uuid.UUID) (DocumentDTO, error) {
	doc, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: looking up for re-extraction: %w", err)
	}
	if doc == nil {
		return DocumentDTO{}, apperr.NotFound("document not found")
	}
	if doc.Origin != OriginInvoice && doc.Origin != OriginPaystub && doc.Origin != OriginLabResult {
		return DocumentDTO{}, apperr.Validation("this document type has no automatic extraction", "origin")
	}
	if s.pandora == nil {
		return DocumentDTO{}, apperr.Validation("no AI provider is configured", "pandora")
	}
	file, err := s.DownloadFile(ctx, id)
	if err != nil {
		return DocumentDTO{}, err
	}

	if err := s.repo.UpdateExtraction(ctx, id, ExtractionResult{Status: ExtractionPending}); err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: marking re-extraction pending: %w", err)
	}
	safeGo(ctx, func() { s.Extract(context.WithoutCancel(ctx), *doc, file.Content) })

	updated, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: re-reading after starting re-extraction: %w", err)
	}
	return toDTO(*updated), nil
}

// Reconcile applies the total the user (or the assistant) confirmed for a statement that
// auto-extraction could not balance. It re-anchors every line of the document to the confirmed
// competência and adds ONE labelled adjustment line for whatever the extraction still missed, so
// the competência month now matches the statement exactly. Idempotent — a prior adjustment line
// is replaced, not stacked.
func (s *Service) Reconcile(ctx context.Context, id uuid.UUID, req ReconcileDocumentRequest) (DocumentDTO, error) {
	doc, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: looking up for reconcile: %w", err)
	}
	if doc == nil {
		return DocumentDTO{}, apperr.NotFound("document not found")
	}
	if doc.Origin != OriginInvoice && doc.Origin != OriginPaystub {
		return DocumentDTO{}, apperr.Validation("only invoice and paystub documents can be reconciled", "origin")
	}
	target, err := money.Add(req.StatementTotal, money.Zero) // normalize scale
	if err != nil {
		return DocumentDTO{}, apperr.Validation("statementTotal must be numeric", "statementTotal")
	}

	closing := firstOfMonthUTC(time.Now())
	switch {
	case req.ClosingMonth != nil:
		closing = firstOfMonthUTC(req.ClosingMonth.Time())
	case doc.StatementClosingDate != nil:
		closing = firstOfMonthUTC(*doc.StatementClosingDate)
	case doc.DocumentDate != nil:
		closing = firstOfMonthUTC(*doc.DocumentDate)
	}

	// Both paths make the document's lines sum to target exactly, so the post-reconcile delta is
	// always zero; what distinguishes the outcomes is whether that took an adjustment line.
	// residual carries the adjustment's own value (zero when none was needed) so the document
	// records how much of the statement automatic extraction never accounted for.
	var residual string
	var lineCount int
	if doc.Origin == OriginInvoice {
		residual, lineCount, err = s.reconcileInvoice(ctx, *doc, closing, target)
	} else {
		residual, lineCount, err = s.reconcilePaystub(ctx, *doc, closing, target)
	}
	if err != nil {
		return DocumentDTO{}, err
	}

	status := ExtractionAdjusted
	if amountsWithin(residual, money.Zero, reconcileToleranceBRL) {
		status = ExtractionReconciled
	}
	res := ExtractionResult{
		StatementTotal:       strptr(target),
		StatementClosingDate: timePtr(closing),
		Status:               status,
		Delta:                strptr(residual),
	}
	if err := s.repo.UpdateExtraction(ctx, id, res); err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: persisting reconcile result: %w", err)
	}
	log.Printf("documents.Reconcile: %s → %d line(s), ajuste %s, competência %s, status %s", id, lineCount, residual, closing.Format("2006-01"), status)

	updated, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: re-reading after reconcile: %w", err)
	}
	return toDTO(*updated), nil
}

// reconcileInvoice rebuilds the document's expense lines at the confirmed competência, drops any
// previous adjustment line, and appends a fresh one for target - sum(real lines). Returns that
// adjustment's value ("0.00" when none was needed) and the real (non-adjustment) line count.
func (s *Service) reconcileInvoice(ctx context.Context, doc Document, closing time.Time, target string) (string, int, error) {
	existing, err := s.expenses.ListByDocument(ctx, doc.ID)
	if err != nil {
		return "", 0, fmt.Errorf("documents: listing invoice lines for reconcile: %w", err)
	}
	closingCivil := civildate.Of(closing)

	realSum := money.Zero
	reqs := make([]expenses.CreateExpenseRequest, 0, len(existing)+1)
	realCount := 0
	for _, e := range existing {
		if e.Description != nil && strings.HasPrefix(*e.Description, adjustmentDescriptionInvoice) {
			continue // drop the previous adjustment; a new one is computed below
		}
		realCount++
		if next, addErr := money.Add(realSum, e.Amount); addErr == nil {
			realSum = next
		}
		orig := e.OriginalDate
		// Real lines keep counting on their purchase date (see internal/platform/cardspend); only
		// the adjustment line, which has no purchase, sits at the competência.
		spend := closingCivil
		if orig != nil {
			desc := ""
			if e.Description != nil {
				desc = *e.Description
			}
			spend = civildate.Of(cardspend.SpendDateFromDescription(time.Time(*orig), desc))
		}
		reqs = append(reqs, expenses.CreateExpenseRequest{
			CategoryID:   e.CategoryID,
			PersonID:     e.PersonID,
			Amount:       e.Amount,
			ExpenseDate:  spend,
			OriginalDate: orig,
			Description:  e.Description,
			Recurring:    e.Recurring,
		})
	}

	residual, _ := money.Sub(target, realSum)
	adjusted := !amountsWithin(residual, money.Zero, reconcileToleranceBRL)
	if adjusted {
		adjDesc := fmt.Sprintf("%s (diferença não identificada na extração)", adjustmentDescriptionInvoice)
		adjDate := closingCivil
		reqs = append(reqs, expenses.CreateExpenseRequest{
			CategoryID:   s.expenseCategoryIDs(ctx)[normaliseCategory(adjustmentCategoryName)],
			PersonID:     doc.PersonID,
			Amount:       residual,
			ExpenseDate:  closingCivil,
			OriginalDate: &adjDate,
			Description:  &adjDesc,
		})
	} else {
		residual = money.Zero
	}

	if _, err := s.expenses.ReplaceForDocument(ctx, doc.ID, reqs); err != nil {
		return "", 0, err
	}
	return residual, realCount, nil
}

func (s *Service) reconcilePaystub(ctx context.Context, doc Document, closing time.Time, target string) (string, int, error) {
	existing, err := s.income.ListByDocument(ctx, doc.ID)
	if err != nil {
		return "", 0, fmt.Errorf("documents: listing paystub lines for reconcile: %w", err)
	}
	closingCivil := civildate.Of(closing)

	realSum := money.Zero
	reqs := make([]income.CreateEntryRequest, 0, len(existing)+1)
	realCount := 0
	for _, e := range existing {
		if e.Description != nil && strings.HasPrefix(*e.Description, adjustmentDescriptionPaystub) {
			continue
		}
		realCount++
		if next, addErr := money.Add(realSum, e.Amount); addErr == nil {
			realSum = next
		}
		orig := e.OriginalDate
		reqs = append(reqs, income.CreateEntryRequest{
			CategoryID:   e.CategoryID,
			PersonID:     e.PersonID,
			Amount:       e.Amount,
			IncomeDate:   closingCivil,
			OriginalDate: orig,
			Description:  e.Description,
		})
	}

	residual, _ := money.Sub(target, realSum)
	adjusted := !amountsWithin(residual, money.Zero, reconcileToleranceBRL)
	if adjusted {
		adjDesc := fmt.Sprintf("%s (diferença não identificada na extração)", adjustmentDescriptionPaystub)
		adjDate := closingCivil
		reqs = append(reqs, income.CreateEntryRequest{
			PersonID:     doc.PersonID,
			Amount:       residual,
			IncomeDate:   closingCivil,
			OriginalDate: &adjDate,
			Description:  &adjDesc,
		})
	} else {
		residual = money.Zero
	}

	if _, err := s.income.ReplaceForDocument(ctx, doc.ID, reqs); err != nil {
		return "", 0, err
	}
	return residual, realCount, nil
}
