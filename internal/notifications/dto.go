package notifications

import (
	"time"

	"github.com/google/uuid"
)

// NotificationDTO is the JSON shape returned to clients.
type NotificationDTO struct {
	ID        uuid.UUID        `json:"id"`
	Type      NotificationType `json:"type"`
	Title     string           `json:"title"`
	Message   string           `json:"message"`
	Link      *string          `json:"link,omitempty"`
	Read      bool             `json:"read"`
	CreatedAt time.Time        `json:"createdAt"`
}

// CreateNotificationRequest is the JSON body accepted by POST /api/notifications. Read always
// starts false — mirrors the DB column default. In practice an internal/admin operation — every
// real NotificationType is scheduler-emitted, not user-authored (see NotificationType doc).
type CreateNotificationRequest struct {
	Type    NotificationType `json:"type" validate:"required,oneof=INSTALLMENT_OVERDUE INSTALLMENT_DUE_SOON BACKUP_COMPLETED BACKUP_FAILED RESTORE_COMPLETED WEEKLY_DIGEST DOCUMENT_DEADLINE OPEN_FINANCE_ACTION"`
	Title   string           `json:"title" validate:"required"`
	Message string           `json:"message" validate:"required"`
	Link    *string          `json:"link"`
}

// ConfigurationDTO is the JSON shape returned by GET /api/notifications/configuration.
type ConfigurationDTO struct {
	ID            uuid.UUID `json:"id"`
	RetentionDays int       `json:"retentionDays"`
	CreatedAt     time.Time `json:"createdAt"`
}

// UpdateConfigurationRequest is the JSON body accepted by PUT /api/notifications/configuration.
type UpdateConfigurationRequest struct {
	RetentionDays int `json:"retentionDays" validate:"required,min=1,max=365"`
}
