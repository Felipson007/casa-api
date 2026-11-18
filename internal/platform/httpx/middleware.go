package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/eventlog"
	"github.com/atlas/atlas-go/internal/platform/logging"
)

// DefaultEventRecorder is the process-wide audit recorder, set once by the composition root
// (main.go). Modules and this middleware record through it; the default discards events so a
// package test (or a deployment before wiring) never depends on the audit module.
var DefaultEventRecorder eventlog.Recorder = eventlog.NopRecorder{}

// SetEventRecorder installs the audit recorder. A nil recorder is ignored (the Nop default stays).
func SetEventRecorder(rec eventlog.Recorder) {
	if rec != nil {
		DefaultEventRecorder = rec
	}
}

type ctxKeyRequestID struct{}
type ctxKeyRequestStart struct{}

// RequestIDFromContext returns the per-request id AccessLog generated, or "" outside a request.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyRequestID{}).(string)
	return id
}

func requestDurationMS(ctx context.Context) int {
	start, ok := ctx.Value(ctxKeyRequestStart{}).(time.Time)
	if !ok || start.IsZero() {
		return 0
	}
	d := time.Since(start).Milliseconds()
	if d < 0 {
		return 0
	}
	return int(d)
}

// recordAuditEvent fills in the request-derived fields a caller didn't set and hands the event to
// the recorder. Never blocks (the production Recorder buffers), so it is safe in the error path.
func recordAuditEvent(r *http.Request, ev eventlog.Event) {
	if DefaultEventRecorder == nil {
		return
	}
	if ev.RequestID == "" {
		ev.RequestID = RequestIDFromContext(r.Context())
	}
	if ev.DurationMS == 0 {
		ev.DurationMS = requestDurationMS(r.Context())
	}
	if u, ok := UserFromContext(r.Context()); ok {
		if ev.UserID == nil {
			id := u.ID
			ev.UserID = &id
		}
		if ev.TenantID == nil && u.TenantID != uuid.Nil {
			tid := u.TenantID
			ev.TenantID = &tid
		}
	}
	if ev.IP == "" {
		ev.IP = ClientIP(r)
	}
	if ev.UserAgent == "" {
		ev.UserAgent = r.UserAgent()
	}
	if ev.Method == "" {
		ev.Method = r.Method
	}
	if ev.Path == "" {
		ev.Path = r.URL.Path
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	DefaultEventRecorder.Record(r.Context(), ev)
}

// HandlerFunc is the shape every module's handler.go writes: decode, validate, call the service,
// respond — and just return an error instead of hand-writing an error response. Wrap is the one
// place that turns a returned error into an HTTP response, mirroring GlobalExceptionHandler.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Wrap adapts a HandlerFunc into a http.HandlerFunc, mapping any returned *apperr.Error to its
// RFC 7807 body and logging it at the level its Kind implies: WARN for an expected business
// rejection, ERROR for anything else — the same split GlobalExceptionHandler.java's comment block
// describes. A handler never writes its own error response.
func Wrap(fn HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		if err == nil {
			return
		}

		logger := logging.FromContext(r.Context())

		var appErr *apperr.Error
		if !errors.As(err, &appErr) {
			appErr = apperr.Internal(err)
		}

		if appErr.IsExpected() {
			logger.Warn(appErr.Message, "type", appErr.Type, "cause", errUnwrap(appErr))
		} else {
			logger.Error(appErr.Message, "type", appErr.Type, "cause", errUnwrap(appErr))
			// Unexpected (5xx) errors are what the Auditoria screen exists for; expected business
			// rejections (4xx) are noise there.
			recordAuditEvent(r, eventlog.Event{
				Type:       eventlog.TypeError,
				Severity:   eventlog.SeverityError,
				StatusCode: appErr.Status(),
				Message:    appErr.Message,
				Detail:     errUnwrap(appErr),
				Metadata:   map[string]any{"tipo": appErr.Type},
			})
		}

		WriteProblem(w, appErr)
	}
}

func errUnwrap(err *apperr.Error) string {
	if err.Err == nil {
		return ""
	}
	return err.Err.Error()
}

// AccessLog logs the start/end of every HTTP request (method, path, status, duration, request_id)
// and attaches a request-scoped logger to the context so any handler/service can retrieve it via
// logging.FromContext without threading a *slog.Logger through every signature.
func AccessLog(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := uuid.NewString()
			reqLogger := base.With("request_id", requestID)
			start := time.Now()
			ctx := logging.WithLogger(r.Context(), reqLogger)
			ctx = context.WithValue(ctx, ctxKeyRequestID{}, requestID)
			ctx = context.WithValue(ctx, ctxKeyRequestStart{}, start)

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r.WithContext(ctx))

			reqLogger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// Recover turns a panic into a logged apperr.Internal response instead of crashing the process —
// the Go analog of a container restart-per-request never being the failure mode for one bad
// handler.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger := logging.FromContext(r.Context())
				err, ok := rec.(error)
				if !ok {
					err = fmt.Errorf("panic: %v", rec)
				}
				logger.Error("panic recovered", "cause", err.Error())
				recordAuditEvent(r, eventlog.Event{
					Type:       eventlog.TypePanic,
					Severity:   eventlog.SeverityCritical,
					StatusCode: http.StatusInternalServerError,
					Message:    err.Error(),
					Detail:     string(debug.Stack()),
					Metadata:   map[string]any{"tipo": "panic"},
				})
				WriteProblem(w, apperr.Internal(err))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Flush delegates to the wrapped ResponseWriter's http.Flusher, when it has one — statusWriter
// embeds http.ResponseWriter as an interface field, so Go's method promotion only covers that
// interface's own methods (Header/Write/WriteHeader); Flush isn't part of it, so without this an
// SSE handler's `w.(http.Flusher)` type assertion would fail even though the real underlying
// ResponseWriter (net/http's) supports it — see internal/ai/handler.go, the one handler that needs
// this.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
