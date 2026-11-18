package reports

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/categories"
	"github.com/atlas/atlas-go/internal/clients"
	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/jobs"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/personinfo"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/settings"
)

// Service holds the business rules — mirrors RelatorioService + RelatorioFinanceiroService +
// PlanilhaExportService + JobService + JobRunnerService's dispatch switch, combined into one
// module since here they share the exact same job queue, storage, and download-serving logic
// (internal/jobs already provides the durable queue/retry/lease machinery Java built by hand).
type Service struct {
	jobsRepo   jobs.Repository
	runner     *jobs.Runner
	storage    *Storage
	clients    *clients.Service
	loans      *loans.Service
	expenses   *expenses.Service
	income     *income.Service
	categories *categories.Service
	people     *people.Service
	// health/personinfo back GenerateEmergencyCardPDF only — every other report only needs people.
	health     *health.Service
	personinfo *personinfo.Service
	settings   settings.Repository
}

func NewService(
	jobsRepo jobs.Repository,
	storage *Storage,
	clientsSvc *clients.Service,
	loansSvc *loans.Service,
	expensesSvc *expenses.Service,
	incomeSvc *income.Service,
	categoriesSvc *categories.Service,
	peopleSvc *people.Service,
	healthSvc *health.Service,
	personinfoSvc *personinfo.Service,
	settingsRepo settings.Repository,
) *Service {
	return &Service{
		jobsRepo: jobsRepo, storage: storage, clients: clientsSvc, loans: loansSvc,
		expenses: expensesSvc, income: incomeSvc, categories: categoriesSvc, people: peopleSvc,
		health: healthSvc, personinfo: personinfoSvc, settings: settingsRepo,
	}
}

// SetRunner wires the Runner after construction — main.go must build the Runner with
// Service.Process as its ProcessFunc, which needs Service to already exist, so this breaks the
// construction cycle: NewService, then jobs.NewRunner(repo, service.Process, ...), then
// service.SetRunner(runner).
func (s *Service) SetRunner(r *jobs.Runner) {
	s.runner = r
}

func (s *Service) create(ctx context.Context, jobType string, payload map[string]any) (JobDTO, error) {
	job, err := s.jobsRepo.Create(ctx, jobType, payload)
	if err != nil {
		return JobDTO{}, fmt.Errorf("reports: creating job: %w", err)
	}
	if s.runner != nil {
		s.runner.Dispatch(ctx, *job) // fast path — the picker (cmd/worker) is the safety net if this is lost
	}
	return toJobDTO(*job), nil
}

// RequestLoanStatement mirrors RelatorioService.solicitarExtrato + RelatorioController's dispatch.
func (s *Service) RequestLoanStatement(ctx context.Context, clientID uuid.UUID, kind string) (JobDTO, error) {
	return s.create(ctx, TypeLoanStatement, map[string]any{
		"clientId": clientID.String(),
		"kind":     normalizeStatementKind(kind),
	})
}

// RequestSpreadsheetExport mirrors PlanilhaExportService.criarJobExportacao.
func (s *Service) RequestSpreadsheetExport(ctx context.Context) (JobDTO, error) {
	return s.create(ctx, TypeSpreadsheetExport, map[string]any{})
}

// RequestFinancialReport mirrors RelatorioFinanceiroService.criarJob.
func (s *Service) RequestFinancialReport(ctx context.Context, yearStart, yearEnd int) (JobDTO, error) {
	if yearEnd == 0 {
		yearEnd = yearStart
	}
	return s.create(ctx, TypeFinancialReport, map[string]any{
		"yearStart": yearStart,
		"yearEnd":   yearEnd,
	})
}

// GetStatus mirrors JobService.getStatus / JobController's GET /api/jobs/{id}.
func (s *Service) GetStatus(ctx context.Context, id uuid.UUID) (JobDTO, error) {
	job, err := s.jobsRepo.FindByID(ctx, id)
	if err != nil {
		return JobDTO{}, fmt.Errorf("reports: looking up job: %w", err)
	}
	if job == nil {
		return JobDTO{}, apperr.NotFound("job not found")
	}
	return toJobDTO(*job), nil
}

var downloadMeta = map[string]struct {
	ext         string
	contentType string
	prefix      string
}{
	TypeLoanStatement:     {"pdf", "application/pdf", "statement"},
	TypeFinancialReport:   {"pdf", "application/pdf", "financial-report"},
	TypeSpreadsheetExport: {"xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "spreadsheet"},
}

// Download mirrors JobService.getDownload + JobController.download's per-type filename. The file
// is deleted from disk as soon as it's read — a report can be downloaded exactly once.
func (s *Service) Download(ctx context.Context, id uuid.UUID) (DownloadedReport, error) {
	job, err := s.jobsRepo.FindByID(ctx, id)
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: looking up job for download: %w", err)
	}
	if job == nil {
		return DownloadedReport{}, apperr.NotFound("job not found")
	}
	if job.Status != jobs.StatusCompleted {
		return DownloadedReport{}, apperr.Validation("the report isn't ready for download yet", "status")
	}
	filePath, _ := job.Result["filePath"].(string)
	if filePath == "" {
		return DownloadedReport{}, apperr.NotFound("this report has already been downloaded or has expired")
	}

	content, err := s.storage.ReadAndDelete(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return DownloadedReport{}, apperr.NotFound("this report has already been downloaded or has expired")
		}
		return DownloadedReport{}, fmt.Errorf("reports: reading report file: %w", err)
	}

	meta := downloadMeta[job.Type]
	return DownloadedReport{
		Content: content, ContentType: meta.contentType,
		Filename: fmt.Sprintf("%s-%s.%s", meta.prefix, job.ID, meta.ext),
	}, nil
}

// Process is the jobs.ProcessFunc dispatched by both the fast path (create) and the picker safety
// net (cmd/worker) — mirrors JobRunnerService.processarNaTransacao's switch on job.tipo. Retry/
// failure/status-transition bookkeeping is entirely internal/jobs' responsibility (Runner.run +
// Repository.MarkCompleted/MarkFailed) — this only ever returns the result map or an error.
func (s *Service) Process(ctx context.Context, job jobs.Job) (map[string]any, error) {
	switch job.Type {
	case TypeLoanStatement:
		return s.processLoanStatement(ctx, job)
	case TypeSpreadsheetExport:
		return s.processSpreadsheetExport(ctx, job)
	case TypeFinancialReport:
		return s.processFinancialReport(ctx, job)
	default:
		return nil, fmt.Errorf("reports: unknown job type %q", job.Type)
	}
}

func companyBranding(s settings.Repository, ctx context.Context) (name, pixKey string) {
	cfg, err := s.Get(ctx)
	if err != nil || cfg == nil {
		return "", ""
	}
	return cfg.CompanyName, cfg.PixKey
}
