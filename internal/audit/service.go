package audit

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Service holds the business rules for the audit trail. An audit log is append-only: entries are
// created when something changes and are never updated or deleted, so there is no Update/Delete.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns audit entries, optionally filtered by tableName and/or rowID.
func (s *Service) List(ctx context.Context, tableName string, rowID *uuid.UUID) ([]AuditLogDTO, error) {
	list, err := s.repo.List(ctx, tableName, rowID)
	if err != nil {
		return nil, fmt.Errorf("audit: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single audit entry, or apperr.NotFound if it doesn't exist.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (AuditLogDTO, error) {
	a, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return AuditLogDTO{}, fmt.Errorf("audit: looking up: %w", err)
	}
	if a == nil {
		return AuditLogDTO{}, apperr.NotFound("audit log not found")
	}
	return toDTO(*a), nil
}

// Create records a new audit entry (the previous state of a row).
func (s *Service) Create(ctx context.Context, req CreateAuditLogRequest) (AuditLogDTO, error) {
	a := AuditLog{
		ID:        uuid.New(),
		TableName: req.TableName,
		RowID:     req.RowID,
		OldData:   req.OldData,
	}
	if err := s.repo.Create(ctx, a); err != nil {
		return AuditLogDTO{}, fmt.Errorf("audit: creating: %w", err)
	}
	return toDTO(a), nil
}
