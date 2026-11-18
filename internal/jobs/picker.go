package jobs

import (
	"context"
	"log/slog"
	"time"
)

// Picker is the safety-net polling loop — the analog of JobPickerScheduler. It repeatedly claims
// runnable jobs (status PENDING, or PROCESSING with an expired lease — see
// Repository.ClaimNext) and dispatches them to a Runner, so a job dispatched via the "fast path"
// that got lost (process crashed before the goroutine ran) or whose worker died mid-processing is
// always eventually retried, without a human noticing first.
type Picker struct {
	repo          Repository
	runner        *Runner
	logger        *slog.Logger
	interval      time.Duration
	leaseDuration time.Duration
}

func NewPicker(repo Repository, runner *Runner, logger *slog.Logger, interval, leaseDuration time.Duration) *Picker {
	return &Picker{repo: repo, runner: runner, logger: logger, interval: interval, leaseDuration: leaseDuration}
}

// Run blocks, polling on a ticker until ctx is cancelled. Each tick drains every currently
// runnable job (not just one) before waiting for the next tick, so a backlog doesn't linger a
// full interval per job.
func (p *Picker) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.drain(ctx)
		}
	}
}

func (p *Picker) drain(ctx context.Context) {
	for {
		job, err := p.repo.ClaimNext(ctx, p.leaseDuration)
		if err != nil {
			p.logger.Error("job picker: claim failed", "cause", err.Error())
			return
		}
		if job == nil {
			return
		}
		p.logger.Info("job picker: claimed job", "job_id", job.ID, "job_type", job.Type)
		p.runner.Dispatch(ctx, *job)
	}
}
