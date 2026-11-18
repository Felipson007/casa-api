package loans

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/notifications"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// dueSoonAlertDays mirrors the old Java default (app.jobs.parcela-a-vencer-dias-antes:3) — an
// installment gets exactly one "due soon" alert, on the day its due date is this many days away.
const dueSoonAlertDays = 3

// MaintenanceRepository is the slice of persistence the overdue-sweep/due-soon-alert steps need —
// kept separate from Repository (used by Service for ordinary request handling, and by
// RolloverRecurringInterest) since these two queries scan the whole installments table ("every
// installment due on date X"), which normal request handling never needs. Defined here, at the
// point of consumption, per Go convention.
type MaintenanceRepository interface {
	// ListOverdueCandidates returns PENDING installments due strictly before `today`.
	ListOverdueCandidates(ctx context.Context, today time.Time) ([]Installment, error)
	MarkInstallmentsOverdue(ctx context.Context, installmentIDs []uuid.UUID) error
	// ListDueSoonCandidates returns PENDING installments due exactly on `targetDate`.
	ListDueSoonCandidates(ctx context.Context, targetDate time.Time) ([]Installment, error)
}

// NotificationSender is the one method MaintenanceService needs from notifications.Service —
// satisfied by the real service in production and a fake in tests.
type NotificationSender interface {
	Create(ctx context.Context, req notifications.CreateNotificationRequest) (notifications.NotificationDTO, error)
}

// MaintenanceReport summarizes one run — logged by whoever schedules this.
type MaintenanceReport struct {
	InstallmentsMarkedOverdue int
	DueSoonAlertsSent         int
}

// MaintenanceService runs the overdue-sweep and due-soon-alert steps of the old Java
// EmprestimoMaintenanceScheduler (marcarParcelasAtrasadas / avisarParcelasAVencer) — separate from
// RolloverRecurringInterest (on Service itself, see service.go), which Hermes plugs directly into
// the jobs/scheduler per D-005's contract. This type is a convenience for wiring the other two
// daily steps the same scheduler can also run; it has no ticker/cron of its own either.
type MaintenanceService struct {
	repo          MaintenanceRepository
	notifications NotificationSender
	now           func() time.Time // overridable in tests
}

func NewMaintenanceService(repo MaintenanceRepository, notifications NotificationSender) *MaintenanceService {
	// civildate.Today().Time(), not time.Now() — see civildate.Today's doc comment: this is the
	// exact bug reported as a wrong portfolio score (an installment due today was being swept as
	// overdue hours before its actual due day even started, in the household's timezone).
	return &MaintenanceService{
		repo: repo, notifications: notifications,
		now: func() time.Time { return civildate.Today().Time() },
	}
}

// RunDailyMaintenance runs the overdue-sweep and due-soon-alert steps and returns a summary.
func (m *MaintenanceService) RunDailyMaintenance(ctx context.Context) (MaintenanceReport, error) {
	var report MaintenanceReport
	today := m.now()

	if err := m.markOverdueInstallments(ctx, today, &report); err != nil {
		return report, fmt.Errorf("loans maintenance: marking overdue: %w", err)
	}
	if err := m.alertDueSoonInstallments(ctx, today, &report); err != nil {
		return report, fmt.Errorf("loans maintenance: due-soon alerts: %w", err)
	}
	return report, nil
}

// markOverdueInstallments mirrors EmprestimoMaintenanceScheduler.marcarParcelasAtrasadas.
func (m *MaintenanceService) markOverdueInstallments(ctx context.Context, today time.Time, report *MaintenanceReport) error {
	overdue, err := m.repo.ListOverdueCandidates(ctx, today)
	if err != nil {
		return err
	}
	if len(overdue) == 0 {
		return nil
	}

	ids := make([]uuid.UUID, len(overdue))
	for i, o := range overdue {
		ids[i] = o.ID
	}
	if err := m.repo.MarkInstallmentsOverdue(ctx, ids); err != nil {
		return err
	}
	report.InstallmentsMarkedOverdue = len(overdue)

	for _, o := range overdue {
		link := fmt.Sprintf("/emprestimos/detalhe?id=%s", o.LoanID)
		if _, err := m.notifications.Create(ctx, notifications.CreateNotificationRequest{
			Type:    notifications.NotificationTypeInstallmentOverdue,
			Title:   "Installment overdue",
			Message: fmt.Sprintf("Installment #%d was due %s and hasn't been paid.", o.InstallmentNumber, o.DueDate.Format("2006-01-02")),
			Link:    &link,
		}); err != nil {
			return fmt.Errorf("notifying overdue installment %s: %w", o.ID, err)
		}
	}
	return nil
}

// alertDueSoonInstallments mirrors EmprestimoMaintenanceScheduler.avisarParcelasAVencer — in-app
// notification only. The old Java also sent email + Telegram for this alert; neither delivery
// channel is implemented here (email has no module at all yet, and internal/integrations
// deliberately doesn't call the real Telegram API — see its package doc), so this is a strict
// subset of the original alert's reach, not a full port.
func (m *MaintenanceService) alertDueSoonInstallments(ctx context.Context, today time.Time, report *MaintenanceReport) error {
	target := today.AddDate(0, 0, dueSoonAlertDays)
	dueSoon, err := m.repo.ListDueSoonCandidates(ctx, target)
	if err != nil {
		return err
	}

	for _, d := range dueSoon {
		link := fmt.Sprintf("/emprestimos/detalhe?id=%s", d.LoanID)
		if _, err := m.notifications.Create(ctx, notifications.CreateNotificationRequest{
			Type:    notifications.NotificationTypeInstallmentDueSoon,
			Title:   "Installment due soon",
			Message: fmt.Sprintf("Installment #%d is due %s.", d.InstallmentNumber, d.DueDate.Format("2006-01-02")),
			Link:    &link,
		}); err != nil {
			return fmt.Errorf("notifying due-soon installment %s: %w", d.ID, err)
		}
		report.DueSoonAlertsSent++
	}
	return nil
}
