package settings

// SettingsDTO is the JSON shape returned to clients.
type SettingsDTO struct {
	CompanyName string `json:"companyName"`
	PixKey      string `json:"pixKey"`
	LogoPath    string `json:"logoPath"`
	UpdatedAt   string `json:"updatedAt"`
}

// UpdateSettingsRequest is the JSON body accepted by PUT /api/settings.
type UpdateSettingsRequest struct {
	CompanyName string `json:"companyName"`
	PixKey      string `json:"pixKey"`
	LogoPath    string `json:"logoPath"`
}
