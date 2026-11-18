// Package backup is the backup scheduler configuration (backup_configuration in the old app) —
// what time of day automatic backups run, and when the last one happened. Hermes-owned per
// decisions/DECISIONS.md D-005 (platform slice).
package backup

import (
	"context"
	"time"
)

// BackupConfig is one backup schedule row.
type BackupConfig struct {
	ID                    string     `json:"id"`
	Hour                  int        `json:"hour"`
	Minute                int        `json:"minute"`
	LastAutomaticBackupAt *time.Time `json:"lastAutomaticBackupAt"`
	CreatedAt             time.Time  `json:"createdAt"`
}

// Repository is the persistence boundary for backup configs.
type Repository interface {
	List(ctx context.Context) ([]BackupConfig, error)
	Create(ctx context.Context, c *BackupConfig) (*BackupConfig, error)
	Get(ctx context.Context, id string) (*BackupConfig, error)
	Update(ctx context.Context, c *BackupConfig) (*BackupConfig, error)
	Delete(ctx context.Context, id string) error
}
