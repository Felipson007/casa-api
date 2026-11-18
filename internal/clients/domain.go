// Package clients is the loan-book counterparty module (clientes in the old app) — owned by
// Claude per decisions/DECISIONS.md D-005. Mirrors internal/categories' shape (the reference
// module): domain/dto/mapper/service/handler/routes.go/postgres, no shared-file edits.
package clients

import (
	"time"

	"github.com/google/uuid"
)

// Client mirrors the clients table (internal/migrations/000001_init_schema.up.sql).
type Client struct {
	ID        uuid.UUID
	Name      string
	Phone     *string
	Email     *string
	CPFCNPJ   *string
	Notes     *string
	Active    bool
	PixKey    *string
	CreatedAt time.Time
}
