package backup

import "time"

// BackupConfigDTO is the JSON shape returned to clients.
type BackupConfigDTO struct {
	ID                    string     `json:"id"`
	Hour                  int        `json:"hour"`
	Minute                int        `json:"minute"`
	LastAutomaticBackupAt *time.Time `json:"lastAutomaticBackupAt"`
	CreatedAt             time.Time  `json:"createdAt"`
}

// CreateBackupConfigRequest is the JSON body accepted by POST /api/backup/configurations.
type CreateBackupConfigRequest struct {
	Hour   int `json:"hour" validate:"min=0,max=23"`
	Minute int `json:"minute" validate:"min=0,max=59"`
}

// UpdateBackupConfigRequest is the JSON body accepted by PUT /api/backup/configurations/{id}.
type UpdateBackupConfigRequest struct {
	Hour   int `json:"hour" validate:"min=0,max=23"`
	Minute int `json:"minute" validate:"min=0,max=59"`
}

func toDTO(c *BackupConfig) BackupConfigDTO {
	return BackupConfigDTO{
		ID:                    c.ID,
		Hour:                  c.Hour,
		Minute:                c.Minute,
		LastAutomaticBackupAt: c.LastAutomaticBackupAt,
		CreatedAt:             c.CreatedAt,
	}
}
