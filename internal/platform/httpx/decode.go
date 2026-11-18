// Package httpx holds the small set of HTTP-boundary helpers every module's handler.go uses, so
// no individual handler hand-rolls JSON decoding, validation, or error-response formatting.
package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-playground/validator/v10"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

var validate = validator.New()

// DecodeAndValidate decodes the request body into T and runs struct-tag validation — the Go
// equivalent of @Valid @RequestBody being "free" on every handler. Validation failures come back
// as a single *apperr.Error carrying every invalid field name, mirroring
// MethodArgumentNotValidException's "erros" property.
func DecodeAndValidate[T any](r *http.Request) (T, error) {
	var body T
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return body, apperr.Validation("the request body is missing or malformed")
	}
	if err := validate.Struct(body); err != nil {
		var fields []string
		for _, fe := range err.(validator.ValidationErrors) {
			fields = append(fields, fe.Field())
		}
		return body, apperr.Validation(fmt.Sprintf("%d field(s) failed validation", len(fields)), fields...)
	}
	return body, nil
}
