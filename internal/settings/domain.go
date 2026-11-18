// Package settings is the application-wide singleton config (app_settings in the old app) —
// company name, PIX key, logo path. Hermes-owned per decisions/DECISIONS.md D-005 (platform
// slice). The table is a singleton (single boolean-primary-key row), so this module exposes
// Get/Update only — no List/Create/Delete.
package settings

import (
	"context"
	"time"
)

// Settings is the singleton app configuration. The DB row always has id=true.
type Settings struct {
	CompanyName string    `json:"companyName"`
	PixKey      string    `json:"pixKey"`
	LogoPath    string    `json:"logoPath"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Repository is the persistence boundary for the settings singleton.
type Repository interface {
	Get(ctx context.Context) (*Settings, error)
	Update(ctx context.Context, s *Settings) error
}
