package settings

import "time"

func toDTO(s *Settings) SettingsDTO {
	return SettingsDTO{
		CompanyName: s.CompanyName,
		PixKey:      s.PixKey,
		LogoPath:    s.LogoPath,
		UpdatedAt:   s.UpdatedAt.Format(time.RFC3339),
	}
}
