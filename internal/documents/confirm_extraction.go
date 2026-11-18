package documents

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/cardspend"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/atlas/atlas-go/internal/platform/money"
)

// ConfirmExtraction is the other half of the Inbox review flow: extractInvoice/extractPaystub/
// extractLabResult (extract.go) stage a PendingExtraction and stop, at ExtractionAwaitingReview,
// without writing to expenses/income/health_records. This is what actually persists — called from
// POST /api/documents/{id}/confirm-extraction once the user has reviewed (and possibly edited)
// the staged draft. req carries the same PendingExtraction shape the document was staged with;
// Kind must match the document's Origin, and the corresponding pointer must be set.
func (s *Service) ConfirmExtraction(ctx context.Context, id uuid.UUID, req PendingExtraction) (DocumentDTO, error) {
	doc, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: looking up for confirm-extraction: %w", err)
	}
	if doc == nil {
		return DocumentDTO{}, apperr.NotFound("document not found")
	}
	if doc.ExtractionStatus == nil || *doc.ExtractionStatus != ExtractionAwaitingReview {
		return DocumentDTO{}, apperr.Validation("document has no pending extraction awaiting review", "id")
	}

	var res ExtractionResult
	switch {
	case req.Kind == PendingExtractionKindFatura && doc.Origin == OriginInvoice && req.Fatura != nil:
		res, err = s.confirmFatura(ctx, *doc, *req.Fatura)
	case req.Kind == PendingExtractionKindHolerite && doc.Origin == OriginPaystub && req.Holerite != nil:
		res, err = s.confirmHolerite(ctx, *doc, *req.Holerite)
	case req.Kind == PendingExtractionKindExame && doc.Origin == OriginLabResult && req.Exame != nil:
		res, err = s.confirmExame(ctx, *doc, *req.Exame)
	default:
		return DocumentDTO{}, apperr.Validation("kind does not match this document's origin, or the matching field is missing", "kind")
	}
	if err != nil {
		return DocumentDTO{}, err
	}

	res.ClearPendingExtraction = true
	if err := s.repo.UpdateExtraction(ctx, id, res); err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: persisting confirm-extraction result: %w", err)
	}
	updated, err := s.repo.FindByID(ctx, id)
	if err != nil || updated == nil {
		return DocumentDTO{}, fmt.Errorf("documents: reloading after confirm-extraction: %w", err)
	}
	return toDTO(*updated), nil
}

// confirmedStatus maps a post-confirmation sum-vs-target comparison to the terminal status.
// NEEDS_INPUT doesn't apply here — that meant "parked, unresolved"; once the user has explicitly
// confirmed (possibly with a residual they know about and accept), ADJUSTED is the accurate label,
// same as the existing Reconcile flow uses it for "matches only because of an adjustment."
func confirmedStatus(sum, target string) (ExtractionStatus, *string) {
	if target == "" {
		return ExtractionReconciled, nil
	}
	delta, err := money.Sub(sum, target)
	if err != nil || amountsWithin(sum, target, reconcileToleranceBRL) {
		return ExtractionReconciled, nil
	}
	return ExtractionAdjusted, &delta
}

func (s *Service) confirmFatura(ctx context.Context, doc Document, f FaturaExtraida) (ExtractionResult, error) {
	if len(f.Items) == 0 {
		return ExtractionResult{}, apperr.Validation("fatura must have at least one item", "fatura.items")
	}
	closing, ok := parseMonthStart(f.ClosingDate)
	if !ok {
		closing = firstOfMonthUTC(time.Now())
	}
	categoryIDs := s.expenseCategoryIDs(ctx)

	sum := money.Zero
	reqs := make([]expenses.CreateExpenseRequest, 0, len(f.Items))
	for i, it := range f.Items {
		amt, ok := parseSignedAmount(it.Amount)
		if !ok {
			return ExtractionResult{}, apperr.Validation(fmt.Sprintf("item %d: amount must be numeric", i), "fatura.items")
		}
		date, ok := parseDate(it.Date)
		if !ok {
			return ExtractionResult{}, apperr.Validation(fmt.Sprintf("item %d: date must be YYYY-MM-DD", i), "fatura.items")
		}
		desc := strings.TrimSpace(it.Description)
		if desc == "" {
			return ExtractionResult{}, apperr.Validation(fmt.Sprintf("item %d: description is required", i), "fatura.items")
		}
		if next, addErr := money.Add(sum, amt); addErr == nil {
			sum = next
		}
		txnDate := date
		reqs = append(reqs, expenses.CreateExpenseRequest{
			CategoryID: categoryIDs[normaliseCategory(it.Category)],
			PersonID:   doc.PersonID,
			Amount:     amt,
			// Counted on the purchase date (installments in the month they are billed), not at the
			// statement's competência — see internal/platform/cardspend.
			ExpenseDate:    civildate.Of(cardspend.SpendDateFromDescription(time.Time(date), desc)),
			OriginalDate:   &txnDate,
			Description:    &desc,
			Recurring:      it.Recurring,
			CategorySource: itemCategorySource(it),
		})
	}
	n, err := s.expenses.ReplaceForDocument(ctx, doc.ID, reqs)
	if err != nil {
		return ExtractionResult{}, fmt.Errorf("documents: confirm-extraction invoice ReplaceForDocument: %w", err)
	}

	status, delta := confirmedStatus(sum, f.InvoiceTotal)
	log.Printf("documents.ConfirmExtraction: invoice %s → %d line(s) persisted, competência %s, status %s", doc.ID, n, closing.Format("2006-01"), status)
	return ExtractionResult{
		StatementTotal:       strptrIfNotEmpty(f.InvoiceTotal),
		StatementClosingDate: &closing,
		Status:               status,
		Delta:                delta,
	}, nil
}

func (s *Service) confirmHolerite(ctx context.Context, doc Document, h HoleriteExtraido) (ExtractionResult, error) {
	if len(h.Lines) == 0 {
		return ExtractionResult{}, apperr.Validation("holerite must have at least one line", "holerite.lines")
	}
	closing := firstOfMonthUTC(time.Now())
	if parsed, ok := parseMonthStart(h.ReferenceMonth); ok {
		closing = parsed
	}
	closingCivil := civildate.Of(closing)

	sum := money.Zero
	reqs := make([]income.CreateEntryRequest, 0, len(h.Lines))
	for i, l := range h.Lines {
		amt, ok := parseSignedAmount(l.Amount)
		if !ok {
			return ExtractionResult{}, apperr.Validation(fmt.Sprintf("line %d: amount must be numeric", i), "holerite.lines")
		}
		desc := strings.TrimSpace(l.Description)
		if desc == "" {
			return ExtractionResult{}, apperr.Validation(fmt.Sprintf("line %d: description is required", i), "holerite.lines")
		}
		if next, addErr := money.Add(sum, amt); addErr == nil {
			sum = next
		}
		txnDate := closingCivil
		reqs = append(reqs, income.CreateEntryRequest{
			PersonID:     doc.PersonID,
			Amount:       amt,
			IncomeDate:   closingCivil,
			OriginalDate: &txnDate,
			Description:  &desc,
		})
	}
	n, err := s.income.ReplaceForDocument(ctx, doc.ID, reqs)
	if err != nil {
		return ExtractionResult{}, fmt.Errorf("documents: confirm-extraction paystub ReplaceForDocument: %w", err)
	}

	status, delta := confirmedStatus(sum, h.NetPay)
	log.Printf("documents.ConfirmExtraction: paystub %s → %d line(s) persisted, competência %s, status %s", doc.ID, n, closing.Format("2006-01"), status)
	return ExtractionResult{
		StatementTotal:       strptrIfNotEmpty(h.NetPay),
		StatementClosingDate: &closing,
		Status:               status,
		Delta:                delta,
	}, nil
}

func (s *Service) confirmExame(ctx context.Context, doc Document, e ExameExtraido) (ExtractionResult, error) {
	if s.health == nil {
		return ExtractionResult{}, apperr.Validation("health module is not configured", "exame")
	}
	if doc.PersonID == nil {
		return ExtractionResult{}, apperr.Validation("document has no linked person", "personId")
	}
	date, ok := parseDate(e.Date)
	if !ok {
		return ExtractionResult{}, apperr.Validation("date must be YYYY-MM-DD", "exame.date")
	}
	assessment := parseAssessmentStatus(e.Assessment)
	if assessment == nil {
		return ExtractionResult{}, apperr.Validation("assessment must be NORMAL, ATTENTION or ALTERED", "exame.assessment")
	}

	// Same idempotency guard extractLabResult used to apply before creating directly — still
	// worth keeping here, the actual point where the record gets created, in case Reextract staged
	// a second draft for a document some other path already confirmed.
	existing, err := s.health.ListRecords(ctx, doc.PersonID, nil)
	if err != nil {
		return ExtractionResult{}, fmt.Errorf("documents: confirm-extraction lab result lookup: %w", err)
	}
	for _, record := range existing {
		if record.DocumentID != nil && *record.DocumentID == doc.ID {
			log.Printf("documents.ConfirmExtraction: lab result already exists for %s, skipping create", doc.ID)
			return ExtractionResult{Status: ExtractionReconciled}, nil
		}
	}

	var organ *string
	if o := strings.TrimSpace(e.Organ); o != "" {
		organ = &o
	}
	notes := e.Notes
	if _, err := s.health.CreateRecord(ctx, health.CreateRecordRequest{
		PersonID:         *doc.PersonID,
		Type:             health.RecordTypeLabResult,
		RecordDate:       date,
		AssessmentStatus: assessment,
		Organ:            organ,
		DocumentID:       &doc.ID,
		Notes:            &notes,
	}); err != nil {
		return ExtractionResult{}, fmt.Errorf("documents: confirm-extraction lab result create: %w", err)
	}

	// Itemized markers (added 2026-09-17): one health_records row per test result, each linked to
	// a health_marker_catalog entry resolved by name — created on first sight, reused after. This
	// is what lets the health screens plot a marker's history across exams, same as the old Java
	// app's registros_saude did before the Go rewrite dropped the granularity (see
	// ExameExtraido.Markers' doc comment).
	if len(e.Markers) > 0 {
		catalog, err := s.markerCatalogIDs(ctx)
		if err != nil {
			return ExtractionResult{}, fmt.Errorf("documents: confirm-extraction marker catalog lookup: %w", err)
		}
		for i, m := range e.Markers {
			nome := strings.TrimSpace(m.Nome)
			if nome == "" {
				return ExtractionResult{}, apperr.Validation(fmt.Sprintf("marker %d: nome is required", i), "exame.markers")
			}
			valor, ok := parseAmount(m.Valor)
			if !ok {
				return ExtractionResult{}, apperr.Validation(fmt.Sprintf("marker %d: valor must be numeric", i), "exame.markers")
			}
			markerID, err := s.resolveMarkerCatalog(ctx, catalog, nome, m)
			if err != nil {
				return ExtractionResult{}, fmt.Errorf("documents: confirm-extraction marker catalog resolve: %w", err)
			}
			var unit *string
			if u := strings.TrimSpace(m.Unidade); u != "" {
				unit = &u
			}
			if _, err := s.health.CreateRecord(ctx, health.CreateRecordRequest{
				PersonID:        *doc.PersonID,
				Type:            health.RecordTypeLabResult,
				RecordDate:      date,
				Amount:          &valor,
				Unit:            unit,
				MarkerCatalogID: &markerID,
				DocumentID:      &doc.ID,
			}); err != nil {
				return ExtractionResult{}, fmt.Errorf("documents: confirm-extraction marker record create: %w", err)
			}
		}
	}

	log.Printf("documents.ConfirmExtraction: lab result → health record created for %s (%d marker(s))", doc.ID, len(e.Markers))
	return ExtractionResult{Status: ExtractionReconciled}, nil
}

// markerCatalogIDs loads every health_marker_catalog entry into a name->ID lookup, keyed the same
// normalised way as expenseCategoryIDs (case/whitespace-insensitive) — reused across every marker
// in one confirm-extraction call instead of round-tripping ListMarkers per marker.
func (s *Service) markerCatalogIDs(ctx context.Context) (map[string]uuid.UUID, error) {
	items, err := s.health.ListMarkers(ctx)
	if err != nil {
		return nil, fmt.Errorf("health: listing markers: %w", err)
	}
	result := make(map[string]uuid.UUID, len(items))
	for _, item := range items {
		result[normaliseCategory(item.Name)] = item.ID
	}
	return result, nil
}

// resolveMarkerCatalog returns the ID of the catalog entry matching m.Nome (case/whitespace-
// insensitive against the preloaded catalog map), creating one on first sight — seeded with this
// occurrence's unit/reference range, which an existing entry's range is never overwritten by (a
// later lab using a different reference range for the same marker doesn't retroactively change
// what "normal" meant for an earlier reading).
func (s *Service) resolveMarkerCatalog(ctx context.Context, catalog map[string]uuid.UUID, nome string, m ExameExtraidoMarcador) (uuid.UUID, error) {
	key := normaliseCategory(nome)
	if id, ok := catalog[key]; ok {
		return id, nil
	}
	var unit, min, max *string
	if u := strings.TrimSpace(m.Unidade); u != "" {
		unit = &u
	}
	if v := strings.TrimSpace(m.ReferenciaMin); v != "" {
		min = &v
	}
	if v := strings.TrimSpace(m.ReferenciaMax); v != "" {
		max = &v
	}
	created, err := s.health.CreateMarker(ctx, health.CreateMarkerCatalogRequest{
		Name: nome, Unit: unit, ReferenceMin: min, ReferenceMax: max,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("health: creating marker catalog entry %q: %w", nome, err)
	}
	catalog[key] = created.ID
	return created.ID, nil
}

func strptrIfNotEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// itemCategorySource validates the provenance carried by a reviewed line, defaulting to AI when a
// category is present (the model chose it) and NONE when the reviewer cleared it. A client could
// send an arbitrary string, so only the known values survive.
func itemCategorySource(item FaturaExtraidaItem) string {
	switch item.CategorySource {
	case expenses.CategorySourceAI, expenses.CategorySourceHistory,
		expenses.CategorySourceManual, expenses.CategorySourceRule:
		return item.CategorySource
	}
	if strings.TrimSpace(item.Category) != "" {
		return expenses.CategorySourceAI
	}
	return expenses.CategorySourceNone
}
