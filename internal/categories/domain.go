// Package categories is the reference module for the Go rewrite: the smallest complete slice of
// the current Java app (see docs/architecture-plan.md), ported first to validate the layering and
// error-handling patterns before the other 24 modules reuse the same shape.
package categories

import (
	"time"

	"github.com/google/uuid"
)

// Type mirrors the Java CategoriaTipo enum, translated to English (architecture-plan.md
// section 9). Nil on a Category means "legacy/global", the same meaning the nullable Java column
// carries.
type Type string

const (
	TypeExpense Type = "EXPENSE"
	TypeIncome  Type = "INCOME"
	TypeAsset   Type = "ASSET"
)

// Category mirrors the categorias/domain/Categoria.java entity, with categoria_pai_id renamed to
// ParentCategoryID per the naming convention.
type Category struct {
	ID               uuid.UUID
	Name             string
	ParentCategoryID *uuid.UUID
	Type             *Type
	CreatedAt        time.Time
}
