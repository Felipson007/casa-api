// Package audit is the generic edit-trail module (audit_logs in the old app) — owned by Hermes
// per the audit report (docs/audit-20260809.md, "platform/infra" row). It records the previous
// state of a row whenever something changes, so the household can review history. There is no
// user_id or action column in the schema — the log is keyed only by (table_name, row_id) and the
// old_data JSON snapshot, matching the Java-era *_historico tables (e.g. pessoa_informacoes_historico).
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AuditLog mirrors the audit_logs table (internal/migrations/000001_init_schema.up.sql).
// It is append-only: once written, a log entry is never updated or deleted.
type AuditLog struct {
	ID        uuid.UUID
	TableName string
	RowID     uuid.UUID
	OldData   map[string]interface{}
	ChangedAt time.Time
}

// Repository is the audit_logs storage port. Implemented by internal/audit/postgres.
type Repository interface {
	Create(ctx context.Context, a AuditLog) error
	List(ctx context.Context, tableName string, rowID *uuid.UUID) ([]AuditLog, error)
	FindByID(ctx context.Context, id uuid.UUID) (*AuditLog, error)
}
