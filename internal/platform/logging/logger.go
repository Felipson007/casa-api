// Package logging provides the one structured logger every request/job carries through its
// call chain, so the app is never "in the dark" about what it did — the same discipline
// GlobalExceptionHandler.java and JobRunnerService.java already apply, just generalized: WARN for
// expected rejections, ERROR for unexpected failures, and every event carries enough context
// (request_id) to answer "which request/job caused this" without guessing.
package logging

import (
	"context"
	"log/slog"
	"os"
)

// New builds the process-wide base logger: structured JSON, so log lines are events with fields,
// never free-form strings.
func New() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

type ctxKey struct{}

// WithLogger returns a context carrying logger, to be retrieved later via FromContext. The HTTP
// middleware (see platform/httpx) calls this once per request with a child logger already
// carrying a request_id field; the job runner does the same once per job.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// FromContext returns the logger attached by WithLogger, or the package-level fallback base
// logger if none was attached — callers never need a nil check.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return logger
	}
	return fallback
}

var fallback = New()
