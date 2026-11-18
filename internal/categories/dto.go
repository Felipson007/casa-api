package categories

import (
	"time"

	"github.com/google/uuid"
)

// CategoryDTO is the JSON shape returned to clients — kept separate from Category (the domain
// struct) the same way the Java module keeps CategoriaDto separate from the Categoria entity.
type CategoryDTO struct {
	ID               uuid.UUID  `json:"id"`
	Name             string     `json:"name"`
	ParentCategoryID *uuid.UUID `json:"parentCategoryId,omitempty"`
	Type             *Type      `json:"type,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
}

// CreateCategoryRequest is the JSON body accepted by POST /api/categories.
type CreateCategoryRequest struct {
	Name             string     `json:"name" validate:"required"`
	ParentCategoryID *uuid.UUID `json:"parentCategoryId"`
	Type             *Type      `json:"type"`
}
