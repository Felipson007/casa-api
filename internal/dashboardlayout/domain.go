// Package dashboardlayout persists each app user's custom dashboard widget arrangement (drag/resize
// positions from the UI's "Editar layout" mode). Scoped per app_user_id, like internal/chat — each
// person's dashboard layout is their own, not shared household data.
//
// This module deliberately treats the layout payload as opaque JSON: it stores and returns exactly
// what react-grid-layout produces on the frontend ([]{i, x, y, w, h}), without decoding it into a Go
// struct. The backend has no reason to understand grid coordinates — that's presentation state, not
// domain data — so it only validates the outer shape (a JSON array) and passes the bytes through.
package dashboardlayout

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Layout is one app user's saved widget arrangement. Items holds the raw JSON array exactly as
// received from the client and returned to it — see package doc.
type Layout struct {
	AppUserID uuid.UUID
	Items     []byte
	UpdatedAt time.Time
}

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	// Get returns the given app user's saved layout. When the user has never saved one, it returns a
	// zero-value Layout (nil Items, zero UpdatedAt) and a nil error — "never saved" is a normal
	// state, not an error.
	Get(ctx context.Context, appUserID uuid.UUID) (Layout, error)
	// Set upserts the given app user's layout and returns the row as persisted.
	Set(ctx context.Context, appUserID uuid.UUID, items []byte) (Layout, error)
}
