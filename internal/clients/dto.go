package clients

import (
	"time"

	"github.com/google/uuid"
)

// ClientDTO is the JSON shape returned to clients (the HTTP kind, not this package's Client).
type ClientDTO struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Phone     *string   `json:"phone,omitempty"`
	Email     *string   `json:"email,omitempty"`
	CPFCNPJ   *string   `json:"cpfCnpj,omitempty"`
	Notes     *string   `json:"notes,omitempty"`
	Active    bool      `json:"active"`
	PixKey    *string   `json:"pixKey,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// CreateClientRequest is the JSON body accepted by POST /api/clients. Active defaults to true at
// creation — mirrors the DB column default — so it isn't part of the create request.
type CreateClientRequest struct {
	Name    string  `json:"name" validate:"required"`
	Phone   *string `json:"phone"`
	Email   *string `json:"email" validate:"omitempty,email"`
	CPFCNPJ *string `json:"cpfCnpj"`
	Notes   *string `json:"notes"`
	PixKey  *string `json:"pixKey"`
}

// UpdateClientRequest is the JSON body accepted by PUT /api/clients/{id}. Unlike create, Active is
// settable here — deactivating a client (rather than hard-deleting) is the expected way to retire
// one that still has historical loans referencing it.
type UpdateClientRequest struct {
	Name    string  `json:"name" validate:"required"`
	Phone   *string `json:"phone"`
	Email   *string `json:"email" validate:"omitempty,email"`
	CPFCNPJ *string `json:"cpfCnpj"`
	Notes   *string `json:"notes"`
	Active  bool    `json:"active"`
	PixKey  *string `json:"pixKey"`
}
