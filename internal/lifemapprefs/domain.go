// Package lifemapprefs persists each app user's preference of which Life Map areas (Identidade,
// Finanças, Saúde, Carreira, Patrimônio, Contas Digitais, Seguros, Fiscal, Contratos, Assinaturas,
// Educação, Garantias — see the frontend's useDashboardPessoal.ts) to hide from the "Mapa da Vida"
// card on the Dashboard. Scoped per app_user_id, like internal/chat and internal/dashboardlayout —
// each person's preference is their own, not shared household data.
//
// Unlike internal/dashboardlayout (which treats its payload as opaque grid coordinates the backend
// has no reason to understand), this module's payload is simple enough — a list of area ids — that
// it's decoded into a real []string in the Service. The backend does not validate ids against a
// known set: the frontend already ignores unknown ids on read, so an area removed or renamed in a
// later release just harmlessly stops matching anything, without needing a migration to prune it.
package lifemapprefs

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Preferences is one app user's saved set of hidden Life Map area ids.
type Preferences struct {
	AppUserID   uuid.UUID
	HiddenAreas []string
	UpdatedAt   time.Time
}

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	// Get returns the given app user's saved preferences. When the user has never saved any, it
	// returns an empty Preferences (empty HiddenAreas, zero UpdatedAt) and a nil error — "never
	// saved" is a normal state, not an error.
	Get(ctx context.Context, appUserID uuid.UUID) (Preferences, error)
	// Set upserts the given app user's hidden area ids and returns the row as persisted.
	Set(ctx context.Context, appUserID uuid.UUID, hiddenAreas []string) (Preferences, error)
}
