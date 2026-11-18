package documents

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/categorize"
	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/income"
)

// fakeHealthRepo is a minimal in-memory health.Repository — enough to exercise confirmExame's
// holistic-record + itemized-marker writes and catalog resolve-or-create without a database.
type fakeHealthRepo struct {
	markers map[uuid.UUID]health.MarkerCatalog
	records map[uuid.UUID]health.Record
}

func newFakeHealthRepo() *fakeHealthRepo {
	return &fakeHealthRepo{markers: map[uuid.UUID]health.MarkerCatalog{}, records: map[uuid.UUID]health.Record{}}
}

func (f *fakeHealthRepo) ListMarkers(context.Context) ([]health.MarkerCatalog, error) {
	list := make([]health.MarkerCatalog, 0, len(f.markers))
	for _, m := range f.markers {
		list = append(list, m)
	}
	return list, nil
}
func (f *fakeHealthRepo) FindMarkerByID(_ context.Context, id uuid.UUID) (*health.MarkerCatalog, error) {
	m, ok := f.markers[id]
	if !ok {
		return nil, nil
	}
	return &m, nil
}
func (f *fakeHealthRepo) CreateMarker(_ context.Context, m health.MarkerCatalog) error {
	f.markers[m.ID] = m
	return nil
}
func (f *fakeHealthRepo) UpdateMarker(_ context.Context, m health.MarkerCatalog) error {
	f.markers[m.ID] = m
	return nil
}
func (f *fakeHealthRepo) DeleteMarker(_ context.Context, id uuid.UUID) error {
	delete(f.markers, id)
	return nil
}
func (f *fakeHealthRepo) ListRecords(_ context.Context, personID, markerCatalogID *uuid.UUID) ([]health.Record, error) {
	var list []health.Record
	for _, r := range f.records {
		if personID != nil && r.PersonID != *personID {
			continue
		}
		if markerCatalogID != nil && (r.MarkerCatalogID == nil || *r.MarkerCatalogID != *markerCatalogID) {
			continue
		}
		list = append(list, r)
	}
	return list, nil
}
func (f *fakeHealthRepo) FindRecordByID(_ context.Context, id uuid.UUID) (*health.Record, error) {
	r, ok := f.records[id]
	if !ok {
		return nil, nil
	}
	return &r, nil
}
func (f *fakeHealthRepo) CreateRecord(_ context.Context, r health.Record) error {
	f.records[r.ID] = r
	return nil
}
func (f *fakeHealthRepo) UpdateRecord(_ context.Context, r health.Record) error {
	f.records[r.ID] = r
	return nil
}
func (f *fakeHealthRepo) DeleteRecord(_ context.Context, id uuid.UUID) error {
	delete(f.records, id)
	return nil
}

// newConfirmTestServiceWithHealth mirrors newConfirmTestService, wired for the EXAME path instead
// of FATURA/HOLERITE.
func newConfirmTestServiceWithHealth(t *testing.T) (*Service, *fakeRepository, *fakeHealthRepo) {
	t.Helper()
	docsRepo := newFakeRepository()
	healthRepo := newFakeHealthRepo()
	svc := NewService(docsRepo, nil, nil, nil, nil, nil, health.NewService(healthRepo))
	return svc, docsRepo, healthRepo
}

// fakeExpensesRepo/fakeIncomeRepo are minimal in-memory expenses.Repository/income.Repository
// implementations — only ReplaceForDocument is exercised by ConfirmExtraction, the rest are
// unused-but-required to satisfy the interface.
type fakeExpensesRepo struct {
	byDocument map[uuid.UUID][]expenses.Expense
}

func (f *fakeExpensesRepo) List(context.Context, *uuid.UUID, *uuid.UUID, string) ([]expenses.Expense, error) {
	return nil, nil
}
func (f *fakeExpensesRepo) FindByID(context.Context, uuid.UUID) (*expenses.Expense, error) {
	return nil, nil
}
func (f *fakeExpensesRepo) Create(context.Context, expenses.Expense) error { return nil }
func (f *fakeExpensesRepo) Update(context.Context, expenses.Expense) error { return nil }
func (f *fakeExpensesRepo) Delete(context.Context, uuid.UUID) error        { return nil }
func (f *fakeExpensesRepo) ListByDocument(_ context.Context, documentID uuid.UUID) ([]expenses.Expense, error) {
	return f.byDocument[documentID], nil
}
func (f *fakeExpensesRepo) ReplaceForDocument(_ context.Context, documentID uuid.UUID, es []expenses.Expense) error {
	if f.byDocument == nil {
		f.byDocument = make(map[uuid.UUID][]expenses.Expense)
	}
	f.byDocument[documentID] = es
	return nil
}
func (f *fakeExpensesRepo) ListInRange(context.Context, time.Time, time.Time) ([]expenses.ExpenseWithCategoryName, error) {
	return nil, nil
}
func (f *fakeExpensesRepo) ListRecurringSince(context.Context, time.Time) ([]expenses.ExpenseWithCategoryName, error) {
	return nil, nil
}
func (f *fakeExpensesRepo) CategoryHistory(context.Context) ([]categorize.Example, error) {
	return nil, nil
}
func (f *fakeExpensesRepo) CategoryIDsByName(context.Context, string) (map[string]uuid.UUID, error) {
	return nil, nil
}
func (f *fakeExpensesRepo) AutoCategorize(context.Context, []expenses.CategoryAssignment) (int, error) {
	return 0, nil
}

type fakeIncomeRepo struct{ byDocument map[uuid.UUID][]income.Entry }

func (f *fakeIncomeRepo) List(context.Context, *uuid.UUID, *uuid.UUID, string) ([]income.Entry, error) {
	return nil, nil
}
func (f *fakeIncomeRepo) FindByID(context.Context, uuid.UUID) (*income.Entry, error) { return nil, nil }
func (f *fakeIncomeRepo) Create(context.Context, income.Entry) error                 { return nil }
func (f *fakeIncomeRepo) Update(context.Context, income.Entry) error                 { return nil }
func (f *fakeIncomeRepo) Delete(context.Context, uuid.UUID) error                    { return nil }
func (f *fakeIncomeRepo) ListByDocument(_ context.Context, documentID uuid.UUID) ([]income.Entry, error) {
	return f.byDocument[documentID], nil
}
func (f *fakeIncomeRepo) ReplaceForDocument(_ context.Context, documentID uuid.UUID, es []income.Entry) error {
	if f.byDocument == nil {
		f.byDocument = make(map[uuid.UUID][]income.Entry)
	}
	f.byDocument[documentID] = es
	return nil
}
func (f *fakeIncomeRepo) ListInRange(context.Context, time.Time, time.Time) ([]income.EntryWithCategoryName, error) {
	return nil, nil
}
func (f *fakeIncomeRepo) CategoryHistory(context.Context) ([]categorize.Example, error) {
	return nil, nil
}
func (f *fakeIncomeRepo) CategoryIDsByName(context.Context, string) (map[string]uuid.UUID, error) {
	return nil, nil
}
func (f *fakeIncomeRepo) AutoCategorize(context.Context, []income.CategoryAssignment) (int, error) {
	return 0, nil
}

func TestConfirmedStatus(t *testing.T) {
	tests := []struct {
		name       string
		sum        string
		target     string
		wantStatus ExtractionStatus
		wantDelta  bool
	}{
		{"no target — nothing to compare", "100.00", "", ExtractionReconciled, false},
		{"matches within tolerance", "100.00", "100.00", ExtractionReconciled, false},
		{"mismatch — adjusted with a delta", "80.00", "100.00", ExtractionAdjusted, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, delta := confirmedStatus(tt.sum, tt.target)
			if status != tt.wantStatus {
				t.Errorf("confirmedStatus(%q, %q) status = %q, want %q", tt.sum, tt.target, status, tt.wantStatus)
			}
			if (delta != nil) != tt.wantDelta {
				t.Errorf("confirmedStatus(%q, %q) delta = %v, want present=%v", tt.sum, tt.target, delta, tt.wantDelta)
			}
		})
	}
}

// newConfirmTestService wires a documents.Service with the fake documents.Repository plus real
// expenses.Service/income.Service backed by the in-memory fakes above — everything
// ConfirmExtraction actually touches, without a database.
func newConfirmTestService(t *testing.T) (*Service, *fakeRepository, *fakeExpensesRepo, *fakeIncomeRepo) {
	t.Helper()
	docsRepo := newFakeRepository()
	expRepo := &fakeExpensesRepo{}
	incRepo := &fakeIncomeRepo{}
	svc := NewService(docsRepo, nil, nil, nil, expenses.NewService(expRepo), income.NewService(incRepo), nil)
	return svc, docsRepo, expRepo, incRepo
}

func TestService_ConfirmExtraction_Fatura_PersistsAndClearsPending(t *testing.T) {
	svc, docsRepo, expRepo, _ := newConfirmTestService(t)
	ctx := context.Background()

	draft := FaturaExtraida{
		ClosingDate:  "2026-09-01",
		InvoiceTotal: "150.00",
		Items: []FaturaExtraidaItem{
			{Amount: "100.00", Date: "2026-09-05", Description: "Mercado", Category: "Alimentação"},
			{Amount: "50.00", Date: "2026-09-10", Description: "Farmácia", Category: "Saúde"},
		},
	}
	envelope, err := json.Marshal(PendingExtraction{Kind: PendingExtractionKindFatura, Fatura: &draft})
	if err != nil {
		t.Fatalf("marshal draft: %v", err)
	}
	pending := string(envelope)
	status := ExtractionAwaitingReview
	docID := uuid.New()
	docsRepo.byID[docID] = Document{
		ID: docID, Origin: OriginInvoice, ExtractionStatus: &status, PendingExtraction: &pending,
	}

	dto, err := svc.ConfirmExtraction(ctx, docID, PendingExtraction{Kind: PendingExtractionKindFatura, Fatura: &draft})
	if err != nil {
		t.Fatalf("ConfirmExtraction() error = %v", err)
	}

	if got := len(expRepo.byDocument[docID]); got != 2 {
		t.Fatalf("expenses persisted = %d, want 2", got)
	}
	if dto.ExtractionStatus == nil || *dto.ExtractionStatus != ExtractionReconciled {
		t.Errorf("ExtractionStatus = %v, want RECONCILED (100+50 == printed total 150)", dto.ExtractionStatus)
	}
	if dto.PendingExtraction != nil {
		t.Errorf("PendingExtraction = %+v, want nil (cleared on confirm)", dto.PendingExtraction)
	}
}

func TestService_ConfirmExtraction_KindMismatchWithOrigin_Rejected(t *testing.T) {
	svc, docsRepo, _, _ := newConfirmTestService(t)
	ctx := context.Background()

	status := ExtractionAwaitingReview
	docID := uuid.New()
	docsRepo.byID[docID] = Document{ID: docID, Origin: OriginPaystub, ExtractionStatus: &status}

	// Fatura payload against a PAYSTUB document — must be rejected, not silently accepted.
	_, err := svc.ConfirmExtraction(ctx, docID, PendingExtraction{
		Kind:   PendingExtractionKindFatura,
		Fatura: &FaturaExtraida{Items: []FaturaExtraidaItem{{Amount: "1.00", Date: "2026-01-01", Description: "x"}}},
	})
	if err == nil {
		t.Fatal("expected an error for kind/origin mismatch, got nil")
	}
}

func TestService_ConfirmExtraction_NotAwaitingReview_Rejected(t *testing.T) {
	svc, docsRepo, _, _ := newConfirmTestService(t)
	ctx := context.Background()

	docID := uuid.New()
	docsRepo.byID[docID] = Document{ID: docID, Origin: OriginInvoice} // ExtractionStatus nil

	_, err := svc.ConfirmExtraction(ctx, docID, PendingExtraction{
		Kind:   PendingExtractionKindFatura,
		Fatura: &FaturaExtraida{Items: []FaturaExtraidaItem{{Amount: "1.00", Date: "2026-01-01", Description: "x"}}},
	})
	if err == nil {
		t.Fatal("expected an error confirming a document with no pending extraction, got nil")
	}
}

// TestService_ConfirmExtraction_Exame_CreatesHolisticAndMarkerRecords regresses the gap found in
// production 2026-09-17: confirmExame used to create only the one holistic assessment record,
// never itemizing the panel the way fatura/holerite itemize their lines — see ExameExtraido.Markers'
// doc comment. Confirming a draft with two markers must produce the holistic record PLUS one
// health_records row per marker, each linked to a resolved-or-created marker catalog entry.
func TestService_ConfirmExtraction_Exame_CreatesHolisticAndMarkerRecords(t *testing.T) {
	svc, docsRepo, healthRepo := newConfirmTestServiceWithHealth(t)
	ctx := context.Background()

	personID := uuid.New()
	draft := ExameExtraido{
		Date: "2026-09-10", Assessment: "NORMAL", Organ: "Fígado", Notes: "Tudo dentro do esperado.",
		Markers: []ExameExtraidoMarcador{
			{Nome: "Colesterol Total", Valor: "185.00", Unidade: "mg/dL", ReferenciaMin: "0", ReferenciaMax: "190"},
			{Nome: "Glicemia de Jejum", Valor: "92.00", Unidade: "mg/dL"},
		},
	}
	envelope, err := json.Marshal(PendingExtraction{Kind: PendingExtractionKindExame, Exame: &draft})
	if err != nil {
		t.Fatalf("marshal draft: %v", err)
	}
	pending := string(envelope)
	status := ExtractionAwaitingReview
	docID := uuid.New()
	docsRepo.byID[docID] = Document{
		ID: docID, Origin: OriginLabResult, PersonID: &personID, ExtractionStatus: &status, PendingExtraction: &pending,
	}

	dto, err := svc.ConfirmExtraction(ctx, docID, PendingExtraction{Kind: PendingExtractionKindExame, Exame: &draft})
	if err != nil {
		t.Fatalf("ConfirmExtraction() error = %v", err)
	}
	if dto.ExtractionStatus == nil || *dto.ExtractionStatus != ExtractionReconciled {
		t.Errorf("ExtractionStatus = %v, want RECONCILED", dto.ExtractionStatus)
	}

	var holistic, markerRecords int
	for _, r := range healthRepo.records {
		if r.DocumentID == nil || *r.DocumentID != docID {
			continue
		}
		if r.MarkerCatalogID == nil {
			holistic++
			if r.AssessmentStatus == nil || *r.AssessmentStatus != health.AssessmentStatusNormal {
				t.Errorf("holistic record AssessmentStatus = %v, want NORMAL", r.AssessmentStatus)
			}
		} else {
			markerRecords++
			if r.Amount == nil {
				t.Errorf("marker record has no Amount")
			}
		}
	}
	if holistic != 1 {
		t.Errorf("holistic records = %d, want 1", holistic)
	}
	if markerRecords != 2 {
		t.Errorf("marker records = %d, want 2", markerRecords)
	}
	if len(healthRepo.markers) != 2 {
		t.Errorf("marker catalog entries created = %d, want 2", len(healthRepo.markers))
	}
}

// TestService_ConfirmExtraction_Exame_ReusesExistingMarkerCatalog confirms a marker name matching
// an existing catalog entry (case/whitespace-insensitive) reuses it instead of creating a
// duplicate — otherwise every re-upload of the same panel type would fragment the same marker
// across N catalog rows, breaking any history/trend view keyed by marker_catalog_id.
func TestService_ConfirmExtraction_Exame_ReusesExistingMarkerCatalog(t *testing.T) {
	svc, docsRepo, healthRepo := newConfirmTestServiceWithHealth(t)
	ctx := context.Background()

	existingID := uuid.New()
	healthRepo.markers[existingID] = health.MarkerCatalog{ID: existingID, Name: "colesterol total"}
	personID := uuid.New()
	draft := ExameExtraido{
		Date: "2026-09-10", Assessment: "NORMAL",
		Markers: []ExameExtraidoMarcador{{Nome: "Colesterol Total", Valor: "185.00"}}, // different case/spacing on purpose
	}
	status := ExtractionAwaitingReview
	docID := uuid.New()
	docsRepo.byID[docID] = Document{ID: docID, Origin: OriginLabResult, PersonID: &personID, ExtractionStatus: &status}

	if _, err := svc.ConfirmExtraction(ctx, docID, PendingExtraction{Kind: PendingExtractionKindExame, Exame: &draft}); err != nil {
		t.Fatalf("ConfirmExtraction() error = %v", err)
	}

	if len(healthRepo.markers) != 1 {
		t.Fatalf("marker catalog entries = %d, want 1 (existing entry reused, not duplicated)", len(healthRepo.markers))
	}
	var markerRecord *health.Record
	for _, r := range healthRepo.records {
		if r.MarkerCatalogID != nil {
			rc := r
			markerRecord = &rc
		}
	}
	if markerRecord == nil || *markerRecord.MarkerCatalogID != existingID {
		t.Fatalf("expected the marker record to link the existing catalog entry %s, got %+v", existingID, markerRecord)
	}
}

// TestService_ConfirmExtraction_Exame_InvalidMarkerValue_Rejected confirms a non-numeric marker
// value blocks the whole confirm (same treatment fatura/holerite items get) rather than silently
// writing a garbage/zero amount — the reviewer edited this by hand, so a mistake here is a typo
// worth surfacing, not something to paper over.
func TestService_ConfirmExtraction_Exame_InvalidMarkerValue_Rejected(t *testing.T) {
	svc, docsRepo, _ := newConfirmTestServiceWithHealth(t)
	ctx := context.Background()

	personID := uuid.New()
	draft := ExameExtraido{
		Date: "2026-09-10", Assessment: "NORMAL",
		Markers: []ExameExtraidoMarcador{{Nome: "Colesterol Total", Valor: "não numérico"}},
	}
	status := ExtractionAwaitingReview
	docID := uuid.New()
	docsRepo.byID[docID] = Document{ID: docID, Origin: OriginLabResult, PersonID: &personID, ExtractionStatus: &status}

	_, err := svc.ConfirmExtraction(ctx, docID, PendingExtraction{Kind: PendingExtractionKindExame, Exame: &draft})
	if err == nil {
		t.Fatal("expected an error for a non-numeric marker value, got nil")
	}
}
