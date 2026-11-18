package loans

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/notifications"
)

// fakeMaintenanceRepository is an in-memory MaintenanceRepository used by unit tests.
type fakeMaintenanceRepository struct {
	installments map[uuid.UUID]Installment
}

func newFakeMaintenanceRepository() *fakeMaintenanceRepository {
	return &fakeMaintenanceRepository{installments: make(map[uuid.UUID]Installment)}
}

func (f *fakeMaintenanceRepository) ListOverdueCandidates(_ context.Context, today time.Time) ([]Installment, error) {
	var result []Installment
	for _, i := range f.installments {
		if i.Status == InstallmentStatusPending && i.DueDate.Before(today) {
			result = append(result, i)
		}
	}
	return result, nil
}

func (f *fakeMaintenanceRepository) MarkInstallmentsOverdue(_ context.Context, installmentIDs []uuid.UUID) error {
	toMark := make(map[uuid.UUID]bool, len(installmentIDs))
	for _, id := range installmentIDs {
		toMark[id] = true
	}
	for id, inst := range f.installments {
		if toMark[id] {
			inst.Status = InstallmentStatusOverdue
			f.installments[id] = inst
		}
	}
	return nil
}

func (f *fakeMaintenanceRepository) ListDueSoonCandidates(_ context.Context, targetDate time.Time) ([]Installment, error) {
	var result []Installment
	for _, i := range f.installments {
		if i.Status == InstallmentStatusPending && i.DueDate.Equal(targetDate) {
			result = append(result, i)
		}
	}
	return result, nil
}

// fakeNotificationSender records every notification it's asked to send, without touching a
// database.
type fakeNotificationSender struct {
	sent []notifications.CreateNotificationRequest
}

func (f *fakeNotificationSender) Create(_ context.Context, req notifications.CreateNotificationRequest) (notifications.NotificationDTO, error) {
	f.sent = append(f.sent, req)
	return notifications.NotificationDTO{ID: uuid.New(), Type: req.Type, Title: req.Title, Message: req.Message}, nil
}

func TestMaintenanceService_MarksOverdueAndNotifies(t *testing.T) {
	repo := newFakeMaintenanceRepository()
	sender := &fakeNotificationSender{}
	loanID := uuid.New()
	overdueID := uuid.New()
	repo.installments[overdueID] = Installment{
		ID: overdueID, LoanID: loanID, InstallmentNumber: 1, DueDate: date(2026, time.January, 1), Status: InstallmentStatusPending,
	}

	service := NewMaintenanceService(repo, sender)
	service.now = func() time.Time { return date(2026, time.January, 15) }

	report, err := service.RunDailyMaintenance(context.Background())
	if err != nil {
		t.Fatalf("RunDailyMaintenance returned error: %v", err)
	}
	if report.InstallmentsMarkedOverdue != 1 {
		t.Fatalf("expected 1 installment marked overdue, got %d", report.InstallmentsMarkedOverdue)
	}
	if repo.installments[overdueID].Status != InstallmentStatusOverdue {
		t.Fatalf("expected installment status OVERDUE, got %v", repo.installments[overdueID].Status)
	}

	found := false
	for _, n := range sender.sent {
		if n.Type == notifications.NotificationTypeInstallmentOverdue {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an INSTALLMENT_OVERDUE notification to be sent")
	}
}

func TestMaintenanceService_AlertsDueSoonExactlyOnTargetDate(t *testing.T) {
	repo := newFakeMaintenanceRepository()
	sender := &fakeNotificationSender{}
	loanID := uuid.New()
	// today + 3 days (dueSoonAlertDays) — should alert.
	dueSoonID := uuid.New()
	repo.installments[dueSoonID] = Installment{
		ID: dueSoonID, LoanID: loanID, InstallmentNumber: 1, DueDate: date(2026, time.January, 18), Status: InstallmentStatusPending,
	}
	// today + 5 days — should NOT alert (not exactly on the target date).
	notYetID := uuid.New()
	repo.installments[notYetID] = Installment{
		ID: notYetID, LoanID: loanID, InstallmentNumber: 2, DueDate: date(2026, time.January, 20), Status: InstallmentStatusPending,
	}

	service := NewMaintenanceService(repo, sender)
	service.now = func() time.Time { return date(2026, time.January, 15) }

	report, err := service.RunDailyMaintenance(context.Background())
	if err != nil {
		t.Fatalf("RunDailyMaintenance returned error: %v", err)
	}
	if report.DueSoonAlertsSent != 1 {
		t.Fatalf("expected exactly 1 due-soon alert, got %d", report.DueSoonAlertsSent)
	}
}
