package goals

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// Repository is defined at the point of consumption (Go idiom). The postgres subpackage
// implements it; nothing here imports postgres.
type Repository interface {
	List(ctx context.Context) ([]Goal, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Goal, error)
	Create(ctx context.Context, g Goal) error
	Update(ctx context.Context, g Goal) error
	Delete(ctx context.Context, id uuid.UUID) error
	// ExistingBalanceIDs returns the subset of ids that reference a real financial_balances row —
	// used to reject a goal that links a balance that doesn't exist.
	ExistingBalanceIDs(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns every goal, ACTIVE first, then by nearest target date.
func (s *Service) List(ctx context.Context) ([]GoalDTO, error) {
	list, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("goals: listing: %w", err)
	}
	today := civildate.Today().Time()
	out := make([]GoalDTO, len(list))
	for i, g := range list {
		out[i] = s.compute(g, today)
	}
	return out, nil
}

func (s *Service) Create(ctx context.Context, req CreateGoalRequest) (GoalDTO, error) {
	if err := s.validateAmounts(req.TargetAmount, req.CurrentAmount, req.MonthlyContribution); err != nil {
		return GoalDTO{}, err
	}
	if err := s.validateFunding(ctx, req.FundingBalanceIDs); err != nil {
		return GoalDTO{}, err
	}
	g := Goal{
		ID:                  uuid.New(),
		Name:                req.Name,
		Kind:                req.Kind,
		TargetAmount:        req.TargetAmount,
		TargetDate:          datePtr(req.TargetDate),
		CurrentAmount:       orZero(req.CurrentAmount),
		MonthlyContribution: req.MonthlyContribution,
		FundingBalanceIDs:   nonNilIDs(req.FundingBalanceIDs),
		Notes:               req.Notes,
		Status:              StatusActive,
	}
	if req.Status != nil {
		g.Status = *req.Status
	}
	if err := s.repo.Create(ctx, g); err != nil {
		return GoalDTO{}, err
	}
	persisted, err := s.repo.FindByID(ctx, g.ID)
	if err != nil {
		return GoalDTO{}, fmt.Errorf("goals: re-reading after create: %w", err)
	}
	return s.compute(*persisted, civildate.Today().Time()), nil
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateGoalRequest) (GoalDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return GoalDTO{}, fmt.Errorf("goals: looking up for update: %w", err)
	}
	if existing == nil {
		return GoalDTO{}, apperr.NotFound("goal not found")
	}
	if err := s.validateAmounts(req.TargetAmount, req.CurrentAmount, req.MonthlyContribution); err != nil {
		return GoalDTO{}, err
	}
	if err := s.validateFunding(ctx, req.FundingBalanceIDs); err != nil {
		return GoalDTO{}, err
	}
	updated := Goal{
		ID:                  id,
		Name:                req.Name,
		Kind:                req.Kind,
		TargetAmount:        req.TargetAmount,
		TargetDate:          datePtr(req.TargetDate),
		CurrentAmount:       orZero(req.CurrentAmount),
		MonthlyContribution: req.MonthlyContribution,
		FundingBalanceIDs:   nonNilIDs(req.FundingBalanceIDs),
		Notes:               req.Notes,
		Status:              req.Status,
		CreatedAt:           existing.CreatedAt,
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return GoalDTO{}, err
	}
	persisted, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return GoalDTO{}, fmt.Errorf("goals: re-reading after update: %w", err)
	}
	return s.compute(*persisted, civildate.Today().Time()), nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("goals: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("goal not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("goals: deleting: %w", err)
	}
	return nil
}

func (s *Service) validateAmounts(target string, current, monthly *string) error {
	t, ok := new(big.Rat).SetString(target)
	if !ok || t.Sign() <= 0 {
		return apperr.Validation("targetAmount must be a positive number", "targetAmount")
	}
	if current != nil {
		if c, ok := new(big.Rat).SetString(*current); !ok || c.Sign() < 0 {
			return apperr.Validation("currentAmount must be zero or more", "currentAmount")
		}
	}
	if monthly != nil {
		if m, ok := new(big.Rat).SetString(*monthly); !ok || m.Sign() < 0 {
			return apperr.Validation("monthlyContribution must be zero or more", "monthlyContribution")
		}
	}
	return nil
}

func (s *Service) validateFunding(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	existing, err := s.repo.ExistingBalanceIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("goals: checking funding balances: %w", err)
	}
	if len(existing) != len(dedupe(ids)) {
		return apperr.Validation("fundingBalanceIds references a financial balance that does not exist", "fundingBalanceIds")
	}
	return nil
}

// compute fills the derived fields. today is passed so tests are deterministic.
func (s *Service) compute(g Goal, today time.Time) GoalDTO {
	target := ratOf(g.TargetAmount)
	accumulated := new(big.Rat).Add(ratOf(g.CurrentAmount), ratOf(g.FundedFromBalances))

	gap := new(big.Rat).Sub(target, accumulated)
	if gap.Sign() < 0 {
		gap = new(big.Rat)
	}

	pct := "0.0"
	if target.Sign() > 0 {
		p := new(big.Rat).Quo(accumulated, target)
		p.Mul(p, big.NewRat(100, 1))
		pct = p.FloatString(1)
	}

	dto := GoalDTO{
		ID:                  g.ID,
		Name:                g.Name,
		Kind:                g.Kind,
		TargetAmount:        money(target),
		TargetDate:          civildatePtr(g.TargetDate),
		CurrentAmount:       money(ratOf(g.CurrentAmount)),
		MonthlyContribution: g.MonthlyContribution,
		FundingBalanceIDs:   nonNilIDs(g.FundingBalanceIDs),
		Notes:               g.Notes,
		Status:              g.Status,
		CreatedAt:           g.CreatedAt,
		UpdatedAt:           g.UpdatedAt,
		FundedFromBalances:  money(ratOf(g.FundedFromBalances)),
		Accumulated:         money(accumulated),
		Gap:                 money(gap),
		ProgressPct:         pct,
	}

	var monthly *big.Rat
	if g.MonthlyContribution != nil {
		if m, ok := new(big.Rat).SetString(*g.MonthlyContribution); ok {
			monthly = m
		}
	}
	if monthly != nil && monthly.Sign() > 0 && gap.Sign() > 0 {
		n := int(math.Ceil(ratToFloat(new(big.Rat).Quo(gap, monthly))))
		dto.MonthsToTarget = &n
	} else if gap.Sign() == 0 {
		zero := 0
		dto.MonthsToTarget = &zero
	}

	if g.TargetDate != nil && monthly != nil {
		months := monthsBetween(today, *g.TargetDate)
		projected := new(big.Rat).Mul(monthly, big.NewRat(int64(maxInt(months, 0)), 1))
		onTrack := projected.Cmp(gap) >= 0
		dto.OnTrackForDate = &onTrack
	}
	return dto
}

// --- helpers ---

func ratOf(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return new(big.Rat)
	}
	return r
}

func money(r *big.Rat) string { return r.FloatString(2) }

func ratToFloat(r *big.Rat) float64 { f, _ := r.Float64(); return f }

func orZero(s *string) string {
	if s == nil || *s == "" {
		return "0"
	}
	return *s
}

func datePtr(d *civildate.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}

func civildatePtr(t *time.Time) *civildate.Date {
	if t == nil {
		return nil
	}
	d := civildate.Of(*t)
	return &d
}

func nonNilIDs(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

func dedupe(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func monthsBetween(from, to time.Time) int {
	return (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month())
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
