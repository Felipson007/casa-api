package aiconnectors

import (
	"context"

	"github.com/google/uuid"
)

// Repository is the per-tenant store of AI connectors (rows live in the tenant's schema, so every
// method runs through db.WithTx with the tenant schema in the context).
type Repository interface {
	List(ctx context.Context) ([]Connector, error)
	Get(ctx context.Context, id uuid.UUID) (*Connector, error)
	Create(ctx context.Context, c Connector) error
	Update(ctx context.Context, c Connector) error
	Delete(ctx context.Context, id uuid.UUID) error
}
