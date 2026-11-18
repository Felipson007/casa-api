package tenancy

import (
	"net/http"

	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// WithTenantSchema wraps the authenticated mux: after AuthMiddleware has put the user in the
// context, it copies the user's tenant schema onto the context so every db.WithTx in the request
// runs inside that schema. A user with no tenant binding (empty schema) is left untouched and falls
// back to the connection default (`public`), which is the owner's schema until the cutover.
//
// Composition order in cmd/api/main.go must be AuthMiddleware(WithTenantSchema(mux)) — auth
// first, then this.
func WithTenantSchema(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := httpx.UserFromContext(r.Context()); ok && u.TenantSchema != "" {
			ctx := db.WithTenantSchema(r.Context(), u.TenantSchema)
			ctx = WithTenantID(ctx, u.TenantID)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
