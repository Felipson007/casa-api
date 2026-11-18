package loans

import (
	"context"
	"fmt"
)

// activity.go ports the "recent activity" feed DashboardResumoService.buscarAtividadesRecentes
// used (AtividadeDto — a movimentação with the client's name resolved). Kept minimal: the old
// code also had a LIMITE_ATIVIDADES_RECENTES cap (4) — see Service.ListRecentActivity's default.

// TransactionWithClientName is a Transaction plus its client's name pre-resolved.
type TransactionWithClientName struct {
	Transaction
	ClientName string
}

// ActivityDTO mirrors AtividadeDto.
type ActivityDTO struct {
	ID         string          `json:"id"`
	ClientName string          `json:"clientName"`
	Type       TransactionType `json:"type"`
	Amount     string          `json:"amount"`
	CreatedAt  string          `json:"createdAt"`
}

// ListRecentActivity returns the most recent loan_transactions across every loan, client name
// resolved — mirrors buscarAtividadesRecentes exactly (LIMITE_ATIVIDADES_RECENTES = 4).
func (s *Service) ListRecentActivity(ctx context.Context, limit int) ([]ActivityDTO, error) {
	rows, err := s.repo.ListRecentTransactions(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("loans: listing recent activity: %w", err)
	}
	result := make([]ActivityDTO, len(rows))
	for i, r := range rows {
		result[i] = ActivityDTO{
			ID: r.ID.String(), ClientName: r.ClientName, Type: r.Type,
			Amount: r.Amount, CreatedAt: r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	return result, nil
}
