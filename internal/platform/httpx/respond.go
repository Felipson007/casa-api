package httpx

import (
	"encoding/json"
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// WriteJSON encodes body as the JSON response, setting the status and content type.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// problemDetail mirrors the shape ProblemDetail.forStatusAndDetail produces, the RFC 7807 body
// GlobalExceptionHandler.java returns for every non-2xx response.
type problemDetail struct {
	Type   string   `json:"type"`
	Title  string   `json:"title"`
	Status int      `json:"status"`
	Detail string   `json:"detail"`
	Errors []string `json:"errors,omitempty"`
}

// WriteProblem writes err as an RFC 7807 problem+json body — the one place every *apperr.Error
// becomes an HTTP response, so no individual handler formats its own error shape.
func WriteProblem(w http.ResponseWriter, err *apperr.Error) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(err.Status())
	_ = json.NewEncoder(w).Encode(problemDetail{
		Type:   "https://atlas.finance/errors/" + err.Type,
		Title:  err.Message,
		Status: err.Status(),
		Detail: err.Message,
		Errors: err.Fields,
	})
}
