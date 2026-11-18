package notifications

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// defaultRetentionDays mirrors notification_configuration.retention_days' DB column default.
const defaultRetentionDays = 7

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	List(ctx context.Context, readOnly *bool) ([]Notification, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Notification, error)
	FindEquivalent(ctx context.Context, notificationType NotificationType, title, message string, link *string) (*Notification, error)
	Create(ctx context.Context, n Notification) error
	MarkRead(ctx context.Context, id uuid.UUID) error
	// MarkAllRead flips every unread notification's Read flag in one statement — backs
	// POST /api/notifications/mark-all-read, added specifically so the frontend's "mark all as
	// read" action never has to fan out one PUT per unread row (it used to).
	MarkAllRead(ctx context.Context) error
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteAll(ctx context.Context) error

	// GetConfiguration returns the single configuration row, or nil if none exists yet.
	GetConfiguration(ctx context.Context) (*Configuration, error)
	// CreateConfiguration inserts the first configuration row (called only when GetConfiguration
	// returns nil — see Service.GetConfiguration's lazy-provisioning below).
	CreateConfiguration(ctx context.Context, c Configuration) error
	// UpdateConfiguration updates the existing configuration row by ID.
	UpdateConfiguration(ctx context.Context, c Configuration) error
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13), except GetConfiguration's lazy-provisioning path, which
// is O(1) reads + at most one O(1) write — still no loop over a collection.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns notifications, optionally filtered to only unread (readOnly == false) or only
// read (readOnly == true) ones; nil returns every notification.
func (s *Service) List(ctx context.Context, readOnly *bool) ([]NotificationDTO, error) {
	list, err := s.repo.List(ctx, readOnly)
	if err != nil {
		return nil, fmt.Errorf("notifications: listing: %w", err)
	}
	return toDTOs(deduplicateRecurringEvents(list)), nil
}

// Schedulers may retry the same run and historically created a fresh row each time. Keep the
// newest occurrence of event-like notifications; operational notifications such as backups are
// intentionally not collapsed because identical messages can represent distinct executions.
func deduplicateRecurringEvents(list []Notification) []Notification {
	seen := make(map[string]struct{})
	result := make([]Notification, 0, len(list))
	for _, notification := range list {
		if notification.Type != NotificationTypeInstallmentDueSoon &&
			notification.Type != NotificationTypeInstallmentOverdue &&
			notification.Type != NotificationTypeDocumentDeadline {
			result = append(result, notification)
			continue
		}
		link := ""
		if notification.Link != nil {
			link = *notification.Link
		}
		key := string(notification.Type) + "\x00" + notification.Title + "\x00" + notification.Message + "\x00" + link
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, notification)
	}
	return result
}

// Get returns a single notification, or apperr.NotFound if it doesn't exist.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (NotificationDTO, error) {
	n, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return NotificationDTO{}, fmt.Errorf("notifications: looking up: %w", err)
	}
	if n == nil {
		return NotificationDTO{}, apperr.NotFound("notification not found")
	}
	return toDTO(*n), nil
}

// Create persists a new notification. Read always starts false.
func (s *Service) Create(ctx context.Context, req CreateNotificationRequest) (NotificationDTO, error) {
	if isRecurringEvent(req.Type) {
		existing, err := s.repo.FindEquivalent(ctx, req.Type, req.Title, req.Message, req.Link)
		if err != nil {
			return NotificationDTO{}, fmt.Errorf("notifications: checking equivalent: %w", err)
		}
		if existing != nil {
			return toDTO(*existing), nil
		}
	}
	n := Notification{
		ID:      uuid.New(),
		Type:    req.Type,
		Title:   req.Title,
		Message: req.Message,
		Link:    req.Link,
		Read:    false,
	}
	if err := s.repo.Create(ctx, n); err != nil {
		return NotificationDTO{}, fmt.Errorf("notifications: creating: %w", err)
	}
	// Re-read rather than mapping the in-memory `n` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above (found systemically across every Create in this codebase by
	// Hermes's HTTP endpoint sweep on loans, msg 83 F-3).
	persisted, err := s.repo.FindByID(ctx, n.ID)
	if err != nil {
		return NotificationDTO{}, fmt.Errorf("notifications: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

func isRecurringEvent(notificationType NotificationType) bool {
	return notificationType == NotificationTypeInstallmentDueSoon ||
		notificationType == NotificationTypeInstallmentOverdue ||
		notificationType == NotificationTypeDocumentDeadline
}

// MarkRead flips a notification's Read flag to true, or returns apperr.NotFound if it doesn't
// exist. There is no "mark unread" — matches the one-way read receipt the old app's UI expects.
func (s *Service) MarkRead(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("notifications: looking up for mark-read: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("notification not found")
	}
	if err := s.repo.MarkRead(ctx, id); err != nil {
		return fmt.Errorf("notifications: marking read: %w", err)
	}
	return nil
}

// MarkAllRead flips every unread notification's Read flag to true in one round trip.
func (s *Service) MarkAllRead(ctx context.Context) error {
	if err := s.repo.MarkAllRead(ctx); err != nil {
		return fmt.Errorf("notifications: marking all read: %w", err)
	}
	return nil
}

// Delete removes a notification by ID, or returns apperr.NotFound if it doesn't exist.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("notifications: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("notification not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("notifications: deleting: %w", err)
	}
	return nil
}

func (s *Service) DeleteAll(ctx context.Context) error {
	if err := s.repo.DeleteAll(ctx); err != nil {
		return fmt.Errorf("notifications: deleting all: %w", err)
	}
	return nil
}

// GetConfiguration returns the (effectively singleton) configuration row, lazily provisioning one
// with the schema's default retentionDays if none exists yet — so callers never have to handle a
// "no configuration" case, and the schema doesn't need a boolean-PK singleton trick (not this
// module's migration to add, D-005) for this to behave like one in practice.
func (s *Service) GetConfiguration(ctx context.Context) (ConfigurationDTO, error) {
	existing, err := s.repo.GetConfiguration(ctx)
	if err != nil {
		return ConfigurationDTO{}, fmt.Errorf("notifications: reading configuration: %w", err)
	}
	if existing != nil {
		return toConfigDTO(*existing), nil
	}

	created := Configuration{ID: uuid.New(), RetentionDays: defaultRetentionDays}
	if err := s.repo.CreateConfiguration(ctx, created); err != nil {
		return ConfigurationDTO{}, fmt.Errorf("notifications: provisioning default configuration: %w", err)
	}
	return toConfigDTO(created), nil
}

// UpdateConfiguration sets RetentionDays on the singleton configuration row, provisioning it
// first via GetConfiguration if it doesn't exist yet.
func (s *Service) UpdateConfiguration(ctx context.Context, req UpdateConfigurationRequest) (ConfigurationDTO, error) {
	current, err := s.GetConfiguration(ctx)
	if err != nil {
		return ConfigurationDTO{}, err
	}

	updated := Configuration{ID: current.ID, RetentionDays: req.RetentionDays, CreatedAt: current.CreatedAt}
	if err := s.repo.UpdateConfiguration(ctx, updated); err != nil {
		return ConfigurationDTO{}, fmt.Errorf("notifications: updating configuration: %w", err)
	}
	return toConfigDTO(updated), nil
}
