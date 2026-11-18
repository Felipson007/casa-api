package jobs

import (
	"context"
	"log/slog"

	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/platform/logging"
)

// ProcessFunc executes one job's work. It receives a context already carrying a job-scoped logger
// (via logging.WithLogger) and the tenant schema of the job's tenant — the same discipline HTTP
// handlers get from httpx.AccessLog, just built once per job instead of once per request.
type ProcessFunc func(ctx context.Context, job Job) (result map[string]any, err error)

// queuedJob carries the tenant-scoping context alongside the job, so a job dispatched from a
// request is processed in the right tenant schema even though the runner detaches it from the
// request's cancellation.
type queuedJob struct {
	ctx context.Context
	job Job
}

// Runner is a bounded worker pool: N goroutines pulling jobs off a channel, each building an
// explicit, immutable per-job context before doing any work. No goroutine here ever captures a
// shared/mutable "current job" variable; the job and its context are passed as parameters, once,
// at dispatch time (docs/architecture-plan.md section 4).
type Runner struct {
	repo        Repository
	process     ProcessFunc
	logger      *slog.Logger
	queue       chan queuedJob
	concurrency int
}

// NewRunner builds a Runner with the given concurrency (number of worker goroutines) and queue
// depth (how many dispatched jobs may be buffered before Dispatch blocks).
func NewRunner(repo Repository, process ProcessFunc, logger *slog.Logger, concurrency, queueDepth int) *Runner {
	return &Runner{
		repo:        repo,
		process:     process,
		logger:      logger,
		queue:       make(chan queuedJob, queueDepth),
		concurrency: concurrency,
	}
}

// Start launches the worker goroutines. It returns immediately; workers run until ctx is
// cancelled.
func (r *Runner) Start(ctx context.Context) {
	for i := 0; i < r.concurrency; i++ {
		go r.worker(ctx)
	}
}

// Dispatch enqueues a job for processing — used both by the "fast path" (right after a job is
// created) and by the picker reclaiming orphaned/pending work. ctx must carry the job's tenant
// schema (db.WithTenantSchema); it is read at dispatch time and re-attached to a detached
// background context, so processing outlives the caller's request without losing the tenant.
func (r *Runner) Dispatch(ctx context.Context, job Job) {
	r.queue <- queuedJob{ctx: ctx, job: job}
}

func (r *Runner) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case q := <-r.queue:
			r.run(q.ctx, q.job)
		}
	}
}

func (r *Runner) run(parent context.Context, job Job) {
	logger := r.logger.With("job_id", job.ID, "job_type", job.Type)

	// Detached from the caller's cancellation (a job may outlive the request that queued it) but
	// carrying the tenant schema, so every db.WithTx in Process lands on the job's tenant.
	jobCtx := context.Background()
	if schema, ok := db.TenantSchemaFromContext(parent); ok {
		jobCtx = db.WithTenantSchema(jobCtx, schema)
	}
	jobCtx = logging.WithLogger(jobCtx, logger)

	logger.Info("job started")
	result, err := r.process(jobCtx, job)
	if err != nil {
		logger.Error("job failed", "cause", err.Error(), "retries", job.Retries+1)
		if markErr := r.repo.MarkFailed(jobCtx, job.ID, err.Error()); markErr != nil {
			logger.Error("job failed to record failure", "cause", markErr.Error())
		}
		return
	}

	if markErr := r.repo.MarkCompleted(jobCtx, job.ID, result); markErr != nil {
		logger.Error("job completed but failed to record result", "cause", markErr.Error())
		return
	}
	logger.Info("job completed")
}
