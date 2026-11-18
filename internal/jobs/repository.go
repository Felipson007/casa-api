// Package jobs is the durable async job queue — the Go analog of JobRunnerService/JobPickerScheduler.
// Unlike an in-memory-only worker pool, every job's state lives in the jobs table, so a process
// restart never silently loses work: see Repository.ClaimNext's lease (locked_until) below.
package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Status mirrors the Java JobStatus enum, translated to English per the naming convention
// (docs/architecture-plan.md section 9).
type Status string

const (
	StatusPending    Status = "PENDING"
	StatusProcessing Status = "PROCESSING"
	StatusCompleted  Status = "COMPLETED"
	StatusFailed     Status = "FAILED"
)

// Job mirrors a row of the jobs table.
type Job struct {
	ID          uuid.UUID
	Type        string
	Payload     map[string]any
	Status      Status
	Retries     int
	MaxRetries  int
	Result      map[string]any
	LockedUntil *time.Time
	CreatedAt   time.Time
}

// Repository is the persistence boundary for jobs.
type Repository interface {
	// Create inserts a new PENDING job of the given type with the given payload and returns it
	// with its generated ID/CreatedAt populated — the row a caller's HTTP handler responds with
	// immediately (still PENDING; the caller never waits for processing to finish).
	Create(ctx context.Context, jobType string, payload map[string]any) (*Job, error)

	// FindByID returns a single job by ID, or nil if it doesn't exist — used for GET /api/jobs/{id}
	// polling and for reading back a COMPLETED job's result.filePath at download time.
	FindByID(ctx context.Context, id uuid.UUID) (*Job, error)

	// ClaimNext atomically picks the next runnable job — status PENDING, or PROCESSING with an
	// expired lease (locked_until < now(), meaning a previous worker crashed mid-job) — and marks
	// it PROCESSING with a fresh lease of leaseDuration. Returns nil, nil when there is no work.
	//
	// This is the fix for the crash-recovery gap plain worker-pool goroutines have: a job stuck at
	// PROCESSING because its worker died is automatically reclaimed once its lease expires,
	// instead of sitting there forever (docs/architecture-plan.md section 4).
	ClaimNext(ctx context.Context, leaseDuration time.Duration) (*Job, error)

	// MarkCompleted records a successful result and flips the job to COMPLETED.
	MarkCompleted(ctx context.Context, jobID uuid.UUID, result map[string]any) error

	// MarkFailed records a failure: increments Retries, and sets status back to PENDING (to be
	// retried) if Retries is still below MaxRetries, or to FAILED with the error message recorded
	// as the result otherwise — mirroring JobRunnerService.processarNaTransacao's retry logic.
	MarkFailed(ctx context.Context, jobID uuid.UUID, causeMessage string) error
}
