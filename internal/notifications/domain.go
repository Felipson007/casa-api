// Package notifications is the in-app notifications module (notificacoes in the old app) — owned
// by Claude per decisions/DECISIONS.md D-005. Covers two tables: Notification (the actual
// notifications shown to the user) and Configuration (a single global settings row — how many
// days notifications are retained before cleanup — same "effectively one row" shape as
// app_settings, just without that table's boolean-PK trick since it's not this module's schema to
// change).
package notifications

import (
	"time"

	"github.com/google/uuid"
)

// NotificationType is a closed enum with no DB CHECK constraint (same situation documents.Origin
// and health.RecordType were in) — confirmed by Hermes against the old Java
// br.com.atlas.notificacoes.domain.NotificacaoTipo. All six values are system/scheduler-emitted
// (NotificacaoScheduler, BackupScheduler, ResumoSemanalScheduler), never user-authored — so
// Create is, in practice, an internal/admin operation rather than an end-user-facing one.
type NotificationType string

const (
	NotificationTypeInstallmentOverdue NotificationType = "INSTALLMENT_OVERDUE"  // PARCELA_ATRASADA
	NotificationTypeInstallmentDueSoon NotificationType = "INSTALLMENT_DUE_SOON" // PARCELA_A_VENCER
	NotificationTypeBackupCompleted    NotificationType = "BACKUP_COMPLETED"     // BACKUP_CONCLUIDO
	NotificationTypeBackupFailed       NotificationType = "BACKUP_FAILED"        // BACKUP_FALHOU
	NotificationTypeRestoreCompleted   NotificationType = "RESTORE_COMPLETED"    // RESTAURACAO_CONCLUIDA
	NotificationTypeWeeklyDigest       NotificationType = "WEEKLY_DIGEST"        // RESUMO_SEMANAL
	NotificationTypeDocumentDeadline   NotificationType = "DOCUMENT_DEADLINE"
	// NotificationTypeOpenFinanceAction: a bank connection needs the household to re-authorise it
	// in Meu Pluggy (expired login, MFA, revoked consent) before syncing can resume.
	NotificationTypeOpenFinanceAction NotificationType = "OPEN_FINANCE_ACTION"
)

// Notification mirrors the notifications table (internal/migrations/000001_init_schema.up.sql).
type Notification struct {
	ID        uuid.UUID
	Type      NotificationType
	Title     string
	Message   string
	Link      *string
	Read      bool
	CreatedAt time.Time
}

// Configuration mirrors the notification_configuration table. Treated as a singleton by this
// module's Service (GetConfiguration/UpdateConfiguration, not a CRUD collection) even though the
// schema itself doesn't enforce single-row-ness the way app_settings' boolean PK does — not this
// module's migration to change (D-005).
type Configuration struct {
	ID            uuid.UUID
	RetentionDays int
	CreatedAt     time.Time
}
