package reports

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/clients"
	"github.com/atlas/atlas-go/internal/jobs"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// fakeJobsRepository is an in-memory jobs.Repository — no Postgres involved. Only the methods this
// package's Request*/GetStatus/Download paths actually call need real behavior; ClaimNext is
// unused by these tests (it's internal/jobs' own concern) but implemented to satisfy the interface.
type fakeJobsRepository struct {
	byID map[uuid.UUID]jobs.Job
}

func newFakeJobsRepository() *fakeJobsRepository {
	return &fakeJobsRepository{byID: make(map[uuid.UUID]jobs.Job)}
}

func (f *fakeJobsRepository) Create(_ context.Context, jobType string, payload map[string]any) (*jobs.Job, error) {
	j := jobs.Job{ID: uuid.New(), Type: jobType, Payload: payload, Status: jobs.StatusPending, MaxRetries: 3, CreatedAt: time.Now()}
	f.byID[j.ID] = j
	return &j, nil
}

func (f *fakeJobsRepository) FindByID(_ context.Context, id uuid.UUID) (*jobs.Job, error) {
	j, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &j, nil
}

func (f *fakeJobsRepository) ClaimNext(_ context.Context, _ time.Duration) (*jobs.Job, error) {
	return nil, nil
}

func (f *fakeJobsRepository) MarkCompleted(_ context.Context, id uuid.UUID, result map[string]any) error {
	j := f.byID[id]
	j.Status = jobs.StatusCompleted
	j.Result = result
	f.byID[id] = j
	return nil
}

func (f *fakeJobsRepository) MarkFailed(_ context.Context, id uuid.UUID, cause string) error {
	j := f.byID[id]
	j.Status = jobs.StatusFailed
	j.Result = map[string]any{"error": cause}
	f.byID[id] = j
	return nil
}

func TestService_RequestLoanStatement_CreatesPendingJobWithPayload(t *testing.T) {
	repo := newFakeJobsRepository()
	svc := NewService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	clientID := uuid.New()

	dto, err := svc.RequestLoanStatement(context.Background(), clientID, StatementKindOutstanding)
	if err != nil {
		t.Fatalf("RequestLoanStatement returned error: %v", err)
	}
	if dto.Status != string(jobs.StatusPending) {
		t.Fatalf("expected PENDING status, got %q", dto.Status)
	}
	if dto.Type != TypeLoanStatement {
		t.Fatalf("expected type %q, got %q", TypeLoanStatement, dto.Type)
	}
	stored := repo.byID[dto.ID]
	if stored.Payload["clientId"] != clientID.String() {
		t.Fatalf("expected payload clientId %q, got %v", clientID.String(), stored.Payload["clientId"])
	}
	if stored.Payload["kind"] != StatementKindOutstanding {
		t.Fatalf("expected payload kind OUTSTANDING, got %v", stored.Payload["kind"])
	}
}

func TestService_RequestLoanStatement_NormalizesUnknownKind(t *testing.T) {
	repo := newFakeJobsRepository()
	svc := NewService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	dto, err := svc.RequestLoanStatement(context.Background(), uuid.New(), "garbage")
	if err != nil {
		t.Fatalf("RequestLoanStatement returned error: %v", err)
	}
	if repo.byID[dto.ID].Payload["kind"] != StatementKindComplete {
		t.Fatalf("expected an unrecognized kind to normalize to COMPLETE, got %v", repo.byID[dto.ID].Payload["kind"])
	}
}

func TestService_RequestFinancialReport_DefaultsYearEndToYearStart(t *testing.T) {
	repo := newFakeJobsRepository()
	svc := NewService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	dto, err := svc.RequestFinancialReport(context.Background(), 2024, 0)
	if err != nil {
		t.Fatalf("RequestFinancialReport returned error: %v", err)
	}
	payload := repo.byID[dto.ID].Payload
	if payload["yearStart"] != 2024 || payload["yearEnd"] != 2024 {
		t.Fatalf("expected yearStart=yearEnd=2024, got %v", payload)
	}
}

func TestService_GetStatus_NotFound(t *testing.T) {
	repo := newFakeJobsRepository()
	svc := NewService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	_, err := svc.GetStatus(context.Background(), uuid.New())
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Download_NotReadyYet(t *testing.T) {
	repo := newFakeJobsRepository()
	svc := NewService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	created, _ := repo.Create(context.Background(), TypeSpreadsheetExport, map[string]any{})

	_, err := svc.Download(context.Background(), created.ID)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
		t.Fatalf("expected a validation apperr.Error for a not-yet-completed job, got %v", err)
	}
}

func TestService_Download_CompletedServesAndDeletesFile(t *testing.T) {
	repo := newFakeJobsRepository()
	storage := NewStorage(t.TempDir())
	svc := NewService(repo, storage, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	created, _ := repo.Create(context.Background(), TypeFinancialReport, map[string]any{})

	path, err := storage.Write(created.ID, "pdf", []byte("%PDF-fake-content"))
	if err != nil {
		t.Fatalf("storage.Write failed: %v", err)
	}
	if err := repo.MarkCompleted(context.Background(), created.ID, map[string]any{"filePath": path, "sizeBytes": 17}); err != nil {
		t.Fatalf("MarkCompleted failed: %v", err)
	}

	file, err := svc.Download(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if string(file.Content) != "%PDF-fake-content" {
		t.Fatalf("expected the stored bytes back, got %q", file.Content)
	}
	if file.ContentType != "application/pdf" {
		t.Fatalf("expected application/pdf, got %q", file.ContentType)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected the report file to be deleted after download, stat error: %v", err)
	}
}

func TestService_Download_AlreadyDownloaded(t *testing.T) {
	repo := newFakeJobsRepository()
	storage := NewStorage(t.TempDir())
	svc := NewService(repo, storage, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	created, _ := repo.Create(context.Background(), TypeFinancialReport, map[string]any{})
	missingPath := filepath.Join(t.TempDir(), "does-not-exist.pdf")
	_ = repo.MarkCompleted(context.Background(), created.ID, map[string]any{"filePath": missingPath})

	_, err := svc.Download(context.Background(), created.ID)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error for an already-downloaded/missing file, got %v", err)
	}
}

func TestFormatBRL(t *testing.T) {
	cases := map[string]string{
		"0.00":       "0,00",
		"5.00":       "5,00",
		"1234.56":    "1.234,56",
		"1234567.89": "1.234.567,89",
		"-1234.56":   "-1.234,56",
		"100":        "100,00",
	}
	for in, want := range cases {
		if got := formatBRL(in); got != want {
			t.Errorf("formatBRL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMonthLabel(t *testing.T) {
	if got := monthLabel(2026, 7); got != "Julho/2026" {
		t.Fatalf("monthLabel(2026, 7) = %q, want Julho/2026", got)
	}
}

func TestMonthAbbrevPt3(t *testing.T) {
	if got := monthAbbrevPt3(1); got != "Jan" {
		t.Fatalf("monthAbbrevPt3(1) = %q, want Jan", got)
	}
}

// TestFirstProjectedCharge covers the loan-preview PDF's recurring-interest slicing — a full
// first-year batch of identical monthly charges must reduce to just the first row, mirroring
// EmprestimoSimulacaoService's own .subList(0,1).
func TestFirstProjectedCharge(t *testing.T) {
	if got := firstProjectedCharge(nil); len(got) != 0 {
		t.Fatalf("expected nil input to stay empty, got %v", got)
	}
	one := []loans.InstallmentCalc{{Number: 1}}
	if got := firstProjectedCharge(one); len(got) != 1 {
		t.Fatalf("expected a single row to pass through unchanged, got %v", got)
	}
	many := []loans.InstallmentCalc{{Number: 1}, {Number: 2}, {Number: 3}}
	if got := firstProjectedCharge(many); len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("expected only the first of several rows, got %v", got)
	}
}

func TestBuildLoanPreviewPDF_Installment(t *testing.T) {
	client := clients.ClientDTO{Name: "Maria Silva"}
	req := loans.CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "1000.00", MonthlyInterestRate: "10",
		PaymentType: loans.PaymentTypeInstallment, InstallmentCount: 3, ContractDate: civildate.Of(time.Now()), DueDay: 5,
	}
	calcs, err := loans.GenerateFixedSchedule(req.PrincipalAmount, req.MonthlyInterestRate, req.InstallmentCount, req.ContractDate.Time(), req.DueDay, 1)
	if err != nil {
		t.Fatalf("GenerateFixedSchedule returned error: %v", err)
	}

	pdf, err := buildLoanPreviewPDF("Atlas", client, req, calcs)
	if err != nil {
		t.Fatalf("buildLoanPreviewPDF returned error: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("expected PDF bytes, got %d bytes", len(pdf))
	}
}

func TestBuildLoanPreviewPDF_RecurringInterest(t *testing.T) {
	client := clients.ClientDTO{Name: "João Souza"}
	req := loans.CreateLoanRequest{
		ClientID: uuid.New(), PrincipalAmount: "2000.00", MonthlyInterestRate: "5",
		PaymentType: loans.PaymentTypeRecurringInterest, ContractDate: civildate.Of(time.Now()), DueDay: 10,
	}
	calcs, err := loans.GenerateRecurringChargesThroughYearEnd(req.PrincipalAmount, req.MonthlyInterestRate, req.ContractDate.Time(), req.DueDay, 1)
	if err != nil {
		t.Fatalf("GenerateRecurringChargesThroughYearEnd returned error: %v", err)
	}
	calcs = firstProjectedCharge(calcs)
	if len(calcs) != 1 {
		t.Fatalf("expected exactly 1 projected charge going into the PDF, got %d", len(calcs))
	}

	pdf, err := buildLoanPreviewPDF("Atlas", client, req, calcs)
	if err != nil {
		t.Fatalf("buildLoanPreviewPDF returned error: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("expected PDF bytes, got %d bytes", len(pdf))
	}
}

func TestBuildReceiptPDF(t *testing.T) {
	client := clients.ClientDTO{Name: "Maria Silva"}
	desc := "Pagamento avulso"
	txn := loans.TransactionDTO{
		ID: uuid.New(), ClientID: uuid.New(), Type: loans.TransactionTypeOneOffPayment,
		Amount: "150.00", PaymentDate: civildate.Today(), Description: &desc,
	}

	pdf, err := buildReceiptPDF("Atlas", client, txn, "Contrato de 01/01/2026 — R$ 1.000,00", "REC-ABCDEF12")
	if err != nil {
		t.Fatalf("buildReceiptPDF returned error: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("expected PDF bytes, got %d bytes", len(pdf))
	}
}

func TestNormalizeStatementKind_LoansDue(t *testing.T) {
	if got := normalizeStatementKind(StatementKindLoansDue); got != StatementKindLoansDue {
		t.Fatalf("expected LOANS_DUE to normalize to itself, got %q", got)
	}
	if got := normalizeStatementKind("XPTO"); got != StatementKindComplete {
		t.Fatalf("expected an unknown kind to normalize to COMPLETE, got %q", got)
	}
}

func TestNextOpenInstallment(t *testing.T) {
	if got := nextOpenInstallment(nil); got != nil {
		t.Fatalf("expected nil for no installments, got %+v", got)
	}

	base := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	allPaid := []loans.InstallmentDTO{
		{DueDate: civildate.Of(base), Status: loans.InstallmentStatusPaid},
		{DueDate: civildate.Of(base.AddDate(0, 1, 0)), Status: loans.InstallmentStatusPaid},
	}
	if got := nextOpenInstallment(allPaid); got != nil {
		t.Fatalf("expected nil when every installment is settled, got %+v", got)
	}

	mixed := []loans.InstallmentDTO{
		{InstallmentNumber: 1, DueDate: civildate.Of(base), Status: loans.InstallmentStatusPaid},
		{InstallmentNumber: 3, DueDate: civildate.Of(base.AddDate(0, 2, 0)), Status: loans.InstallmentStatusPending},
		{InstallmentNumber: 2, DueDate: civildate.Of(base.AddDate(0, 1, 0)), Status: loans.InstallmentStatusOverdue},
	}
	got := nextOpenInstallment(mixed)
	if got == nil || got.InstallmentNumber != 2 {
		t.Fatalf("expected the earliest open installment (#2), got %+v", got)
	}
}

func TestBuildLoanStatementPDF_LoansDue(t *testing.T) {
	client := clients.ClientDTO{Name: "Maicon"}
	active := uuid.New()
	paidOff := uuid.New()
	base := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	d := statementData{
		companyName: "Atlas", client: client, kind: StatementKindLoansDue,
		loans: []loans.LoanDTO{
			{ID: paidOff, Status: loans.StatusPaidOff, OutstandingBalance: "0.00", MonthlyInterestRate: "7.500000", ContractDate: civildate.Of(base)},
			{ID: active, Status: loans.StatusActive, OutstandingBalance: "5000.00", MonthlyInterestRate: "7.500000", ContractDate: civildate.Of(base.AddDate(0, 1, 0))},
		},
		installmentsByLoan: map[uuid.UUID][]loans.InstallmentDTO{
			active: {
				{InstallmentNumber: 1, DueDate: civildate.Of(base.AddDate(0, 2, 0)), Status: loans.InstallmentStatusPaid, InterestAmount: "375.00", InstallmentAmount: "375.00"},
				{InstallmentNumber: 2, DueDate: civildate.Of(base.AddDate(0, 3, 0)), Status: loans.InstallmentStatusPending, InterestAmount: "375.00", InstallmentAmount: "375.00"},
			},
		},
	}

	pdf, err := buildLoanStatementPDF(d)
	if err != nil {
		t.Fatalf("buildLoanStatementPDF returned error: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("expected PDF bytes, got %d bytes", len(pdf))
	}

	// A client whose only loan is already paid off renders the empty-state path, still a valid PDF.
	empty := d
	empty.loans = []loans.LoanDTO{d.loans[0]}
	emptyPDF, err := buildLoanStatementPDF(empty)
	if err != nil {
		t.Fatalf("buildLoanStatementPDF (empty) returned error: %v", err)
	}
	if !bytes.HasPrefix(emptyPDF, []byte("%PDF")) {
		t.Fatalf("expected PDF bytes for the empty case, got %d bytes", len(emptyPDF))
	}
	if len(emptyPDF) >= len(pdf) {
		t.Fatalf("expected the one-row report (%d bytes) to be larger than the empty-state report (%d bytes)", len(pdf), len(emptyPDF))
	}
}

func TestBuildLoanStatementPDF_Outstanding_HasTotals(t *testing.T) {
	client := clients.ClientDTO{Name: "Maicon"}
	d := statementData{
		companyName: "Atlas", client: client, kind: StatementKindOutstanding,
		currentMonth: loans.YearMonth{Year: 2026, Month: 9},
		nextMonth:    loans.YearMonth{Year: 2026, Month: 10},
		currentMonthVigentes: []loans.LoanSummaryDTO{
			{ClientName: "Maicon", InterestThisMonth: "75.00", OutstandingBalance: "1000.00", Status: loans.MonthStatusPending},
			{ClientName: "Maicon", InterestThisMonth: "90.00", OutstandingBalance: "1200.00", Status: loans.MonthStatusPending},
		},
	}

	pdf, err := buildLoanStatementPDF(d)
	if err != nil {
		t.Fatalf("buildLoanStatementPDF returned error: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("expected PDF bytes, got %d bytes", len(pdf))
	}

	// The next-month table is empty; adding the current-month rows must make the report larger.
	empty := d
	empty.currentMonthVigentes = nil
	emptyPDF, err := buildLoanStatementPDF(empty)
	if err != nil {
		t.Fatalf("buildLoanStatementPDF (empty) returned error: %v", err)
	}
	if len(emptyPDF) >= len(pdf) {
		t.Fatalf("expected the report with rows+totals (%d bytes) to be larger than the empty one (%d bytes)", len(pdf), len(emptyPDF))
	}
}

func TestEffectiveMonthlyRateBR(t *testing.T) {
	cases := []struct{ interest, balance, want string }{
		{"100.00", "1000.00", "10,00%"}, // pre-renegotiation month keeps its old 10%
		{"37.50", "500.00", "7,50%"},    // post-renegotiation month at the new 7.5%
		{"150.00", "2000.00", "7,50%"},
		{"0.00", "0.00", "—"},  // paid-off / no installment
		{"75.00", "0.00", "—"}, // guard against divide-by-zero
		{"", "1000.00", "—"},   // unparseable
	}
	for _, c := range cases {
		if got := effectiveMonthlyRateBR(c.interest, c.balance); got != c.want {
			t.Errorf("effectiveMonthlyRateBR(%q, %q) = %q, want %q", c.interest, c.balance, got, c.want)
		}
	}
}
