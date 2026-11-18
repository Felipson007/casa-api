package documents

import (
	"context"
	"fmt"
	"time"

	"github.com/atlas/atlas-go/internal/notifications"
)

type DeadlineNotificationSender interface {
	Create(context.Context, notifications.CreateNotificationRequest) (notifications.NotificationDTO, error)
}

type DeadlineService struct {
	repo          Repository
	notifications DeadlineNotificationSender
}

func NewDeadlineService(repo Repository, sender DeadlineNotificationSender) *DeadlineService {
	return &DeadlineService{repo: repo, notifications: sender}
}

func (s *DeadlineService) Run(ctx context.Context, today time.Time) (int, error) {
	items, err := s.repo.List(ctx, nil, nil, nil, "")
	if err != nil {
		return 0, fmt.Errorf("documents: listing deadlines: %w", err)
	}
	today = dateOnly(today, today.Location())
	thresholds := map[int]struct{}{90: {}, 60: {}, 30: {}, 15: {}, 7: {}}
	sent := 0
	for _, item := range items {
		if item.ExpirationDate == nil {
			continue
		}
		days := int(dateOnly(*item.ExpirationDate, today.Location()).Sub(today).Hours() / 24)
		if _, ok := thresholds[days]; !ok {
			continue
		}
		title := "Vencimento de contrato em breve"
		name := item.OriginalFilename
		if item.Description != nil && *item.Description != "" {
			name = *item.Description
		}
		message := fmt.Sprintf("%s vence em %d dias, em %s.", name, days, item.ExpirationDate.Format("02/01/2006"))
		link := "/contratos"
		if _, err := s.notifications.Create(ctx, notifications.CreateNotificationRequest{Type: notifications.NotificationTypeDocumentDeadline, Title: title, Message: message, Link: &link}); err != nil {
			return sent, fmt.Errorf("documents: creating deadline notification: %w", err)
		}
		sent++
	}
	return sent, nil
}

func dateOnly(value time.Time, location *time.Location) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, location)
}
