package audit

import (
	"github.com/google/uuid"
)

// AuditLogDTO is the JSON shape returned to clients.
type AuditLogDTO struct {
	ID        uuid.UUID              `json:"id"`
	TableName string                 `json:"tableName"`
	RowID     uuid.UUID              `json:"rowId"`
	OldData   map[string]interface{} `json:"oldData"`
	ChangedAt string                 `json:"changedAt"`
}

// ListAuditLogsRequest is the query accepted by GET /api/audit-logs.
type ListAuditLogsRequest struct {
	TableName string    `query:"tableName"`
	RowID     uuid.UUID `query:"rowId"`
}

// CreateAuditLogRequest is the JSON body accepted by POST /api/audit-logs.
type CreateAuditLogRequest struct {
	TableName string                 `json:"tableName" validate:"required"`
	RowID     uuid.UUID              `json:"rowId" validate:"required"`
	OldData   map[string]interface{} `json:"oldData" validate:"required"`
}
