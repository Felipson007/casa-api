package auditevents

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/atlas/atlas-go/internal/platform/eventlog"
)

const (
	// recorderBuffer absorbs bursts (a page load firing several failing queries at once) without
	// blocking handlers. Overflow drops the newest event and logs a warning.
	recorderBuffer = 512
	// insertTimeout bounds each background write; a detached context keeps the event flowing even
	// after the originating request has been cancelled.
	insertTimeout = 5 * time.Second
	// purgeEvery is how often the retention job runs; retention itself is configurable
	// (AUDIT_RETENTION_DAYS, default 3 days).
	purgeEvery = time.Hour
)

// eventStore is the slice of Service the recorder needs. Declaring it here (consumer side) keeps
// the recorder unit-testable without a Postgres-backed Service.
type eventStore interface {
	Record(ctx context.Context, ev eventlog.Event) error
	PurgeOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// Recorder is the eventlog.Recorder wired into the platform. Record never blocks the request
// goroutine: it buffers and drops when full. A background worker persists events on a detached
// context and prunes anything past the retention window.
type Recorder struct {
	service   eventStore
	logger    *slog.Logger
	retention time.Duration

	events chan eventlog.Event
	stop   chan struct{}
	done   chan struct{}

	closeOnce sync.Once
}

// NewRecorder builds a Recorder. retention <= 0 disables pruning.
func NewRecorder(service *Service, logger *slog.Logger, retention time.Duration) *Recorder {
	return newRecorder(service, logger, retention)
}

func newRecorder(service eventStore, logger *slog.Logger, retention time.Duration) *Recorder {
	return &Recorder{
		service:   service,
		logger:    logger,
		retention: retention,
		events:    make(chan eventlog.Event, recorderBuffer),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// Record implements eventlog.Recorder. Never blocks: a full buffer drops the event rather than
// delaying the request it is auditing.
func (r *Recorder) Record(_ context.Context, ev eventlog.Event) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	select {
	case r.events <- ev:
	default:
		r.logger.Warn("auditevents: buffer full, dropping event", "type", ev.Type, "path", ev.Path)
	}
}

// Start launches the persistence worker and the retention ticker. It returns immediately; call
// Close to stop and drain.
func (r *Recorder) Start(ctx context.Context) {
	go r.run(ctx)
}

func (r *Recorder) run(ctx context.Context) {
	defer close(r.done)
	// Prune once at startup so retention is enforced even by a process that only ever restarts.
	r.purge()

	ticker := time.NewTicker(purgeEvery)
	defer ticker.Stop()

	for {
		select {
		case ev := <-r.events:
			r.persist(ev)
		case <-ticker.C:
			r.purge()
		case <-r.stop:
			r.drain()
			return
		case <-ctx.Done():
			r.drain()
			return
		}
	}
}

func (r *Recorder) drain() {
	for {
		select {
		case ev := <-r.events:
			r.persist(ev)
		default:
			return
		}
	}
}

func (r *Recorder) persist(ev eventlog.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), insertTimeout)
	defer cancel()
	if err := r.service.Record(ctx, ev); err != nil {
		r.logger.Warn("auditevents: persisting event failed", "type", ev.Type, "cause", err.Error())
	}
}

func (r *Recorder) purge() {
	if r.retention <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), insertTimeout)
	defer cancel()
	cutoff := time.Now().Add(-r.retention)
	n, err := r.service.PurgeOlderThan(ctx, cutoff)
	if err != nil {
		r.logger.Warn("auditevents: retention purge failed", "cause", err.Error())
		return
	}
	if n > 0 {
		r.logger.Info("auditevents: pruned expired events", "count", n, "retention", r.retention.String())
	}
}

// Close stops the worker, draining buffered events first. Safe to call more than once.
func (r *Recorder) Close() {
	r.closeOnce.Do(func() { close(r.stop) })
	<-r.done
}
