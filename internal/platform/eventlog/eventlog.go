// Package eventlog defines the audit-event contract shared by the platform middleware (httpx)
// and the persistence module (internal/auditevents). It lives in platform so neither side depends
// on the other: httpx records through this interface without importing a SQL driver, and
// auditevents implements Recorder without importing httpx — the same consumer-defined-port idiom
// the domain Repository interfaces use elsewhere.
package eventlog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Type classifies an audit record. It is a small closed set so the audit screen can group/filter
// without guessing at free-form strings.
type Type string

const (
	// TypeLoginSuccess is a completed sign-in (password or Google, after any second factor).
	TypeLoginSuccess Type = "login_success"
	// TypeLoginFailure is a rejected sign-in (bad credentials, unknown account, bad MFA code).
	TypeLoginFailure Type = "login_failure"
	// TypeLoginMFARequired marks a correct password with a pending second-factor challenge — not
	// yet a session, so it is informational rather than a success.
	TypeLoginMFARequired Type = "login_mfa_required"
	// TypeLogout is a session revocation.
	TypeLogout Type = "logout"
	// TypeError is an unexpected server-side error (a 5xx the application produced).
	TypeError Type = "error"
	// TypePanic is a recovered panic.
	TypePanic Type = "panic"
	// TypeClientError is an uncaught exception reported by the frontend.
	TypeClientError Type = "client_error"
)

// Severity drives emphasis in the audit screen. It is stored explicitly (not derived on read) so
// filters and future retention rules stay simple.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// Event is one audit record. Fields beyond the core (Type/Severity/CreatedAt) are best-effort
// request context and may be empty when the event did not originate from an HTTP request.
type Event struct {
	Type       Type
	Severity   Severity
	UserID     *uuid.UUID
	UserEmail  string
	TenantID   *uuid.UUID
	IP         string
	UserAgent  string
	Method     string
	Path       string
	StatusCode int
	RequestID  string
	// Message is the short, human-readable summary shown in lists/toasts.
	Message string
	// Detail is the long form (error chain, stack trace) shown only in the detail view.
	Detail     string
	Metadata   map[string]any
	DurationMS int
	CreatedAt  time.Time
}

// Recorder persists audit events. Implementations MUST be safe for concurrent use and MUST NOT
// block the request goroutine: the production implementation buffers and drops on overflow, since
// an audit trail that degrades a request (or recurses into itself) is worse than one with gaps.
type Recorder interface {
	Record(ctx context.Context, ev Event)
}

// NopRecorder discards everything — the default before wiring (and in tests).
type NopRecorder struct{}

// Record implements Recorder.
func (NopRecorder) Record(context.Context, Event) {}
