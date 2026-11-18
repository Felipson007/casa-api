package lifemapprefs

import "time"

// PreferencesDTO is the JSON shape returned by GET/PUT /api/life-map/preferences.
type PreferencesDTO struct {
	HiddenAreas []string  `json:"hiddenAreas"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// SetPreferencesRequest is the PUT body.
type SetPreferencesRequest struct {
	HiddenAreas []string `json:"hiddenAreas"`
}
