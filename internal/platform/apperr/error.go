// Package apperr is the Go analog of GlobalExceptionHandler: a single, small set of error kinds
// that every handler returns instead of hand-writing an HTTP response, so exactly one place
// (httpx.Wrap) maps them to an RFC 7807 problem+json body — and to the right log level, since an
// expected rejection (not found, validation) is a WARN, never an ERROR.
package apperr

import "net/http"

// Kind classifies an Error for HTTP-status mapping and log-level selection. It intentionally
// mirrors the exception types GlobalExceptionHandler.java handles, not a generic taxonomy.
type Kind int

const (
	KindNotFound Kind = iota
	KindValidation
	KindRateLimited
	KindForbidden
	KindUnauthorized
	KindConflict
	KindInternal
)

// httpStatus maps each Kind to the same HTTP status GlobalExceptionHandler.java returns for its
// closest equivalent exception type.
var httpStatus = map[Kind]int{
	KindNotFound:     http.StatusNotFound,
	KindValidation:   http.StatusBadRequest,
	KindRateLimited:  http.StatusTooManyRequests,
	KindForbidden:    http.StatusForbidden,
	KindUnauthorized: http.StatusUnauthorized,
	KindConflict:     http.StatusConflict,
	KindInternal:     http.StatusInternalServerError,
}

// Error is the one error shape every layer above the repository boundary works with. Fields is
// only populated for KindValidation, mirroring the "erros" property GlobalExceptionHandler adds
// for MethodArgumentNotValidException.
type Error struct {
	Kind    Kind
	Type    string // stable English slug, e.g. "resource-not-found" — mirrors ProblemDetail's type URI
	Message string
	Fields  []string
	Err     error // wrapped cause; logged, never serialized to the client
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// Status returns the HTTP status this error's Kind maps to.
func (e *Error) Status() int {
	if s, ok := httpStatus[e.Kind]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// IsExpected reports whether this Kind represents an expected business-rule rejection (logged at
// WARN) as opposed to a genuinely unexpected failure (logged at ERROR) — the same split
// GlobalExceptionHandler's comment block describes.
func (e *Error) IsExpected() bool {
	return e.Kind != KindInternal
}

// NotFound builds a KindNotFound error — the analog of ResourceNotFoundException.
func NotFound(message string) *Error {
	return &Error{Kind: KindNotFound, Type: "resource-not-found", Message: message}
}

// Validation builds a KindValidation error carrying the list of invalid field names, the analog
// of MethodArgumentNotValidException's "erros" property.
func Validation(message string, fields ...string) *Error {
	return &Error{Kind: KindValidation, Type: "validation-failed", Message: message, Fields: fields}
}

// RateLimited builds a KindRateLimited error — the analog of RateLimitExceededException.
func RateLimited(message string) *Error {
	return &Error{Kind: KindRateLimited, Type: "rate-limit-exceeded", Message: message}
}

// Forbidden builds a KindForbidden error — the analog of Spring Security's AccessDeniedException.
// The message passed here should already be the generic, client-safe message; log the real cause
// via Err, not Message.
func Forbidden(message string) *Error {
	return &Error{Kind: KindForbidden, Type: "access-denied", Message: message}
}

// Unauthorized builds a KindUnauthorized error — the analog of AuthenticationException.
func Unauthorized(message string) *Error {
	return &Error{Kind: KindUnauthorized, Type: "invalid-authentication", Message: message}
}

// Conflict builds a KindConflict error — the analog of DataIntegrityViolationException mapping
// (unique/foreign-key violations translated at the repository boundary).
func Conflict(message string) *Error {
	return &Error{Kind: KindConflict, Type: "data-conflict", Message: message}
}

// Internal wraps an unexpected error as KindInternal — the catch-all analog of handleGeneric.
// The wrapped cause is logged in full; the client only ever sees the generic Message.
func Internal(cause error) *Error {
	return &Error{Kind: KindInternal, Type: "internal-error", Message: "an unexpected error occurred", Err: cause}
}
