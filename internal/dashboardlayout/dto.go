package dashboardlayout

import (
	"encoding/json"
	"time"
)

// LayoutDTO is the JSON shape returned by GET/PUT /api/dashboard/layout.
type LayoutDTO struct {
	Layout    json.RawMessage `json:"layout"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// SetLayoutRequest is the PUT body. Layout isn't struct-tag-validated (httpx.DecodeAndValidate has
// no "is a JSON array" rule) — Service.Set checks that explicitly before writing.
type SetLayoutRequest struct {
	Layout json.RawMessage `json:"layout"`
}
