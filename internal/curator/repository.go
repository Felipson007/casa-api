package curator

import (
	"context"
	"time"
)

type Repository interface {
	WasCompleted(ctx context.Context, date time.Time) (bool, error)
	SaveSnapshot(ctx context.Context, date time.Time, insights []Insight, reclassified int) error
	ListInsights(ctx context.Context) ([]Insight, error)
}
