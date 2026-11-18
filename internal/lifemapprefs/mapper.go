package lifemapprefs

func toDTO(p Preferences) PreferencesDTO {
	hidden := p.HiddenAreas
	if hidden == nil {
		// Never let this serialize as JSON null — see docs/code-quality: a nil slice reaching the
		// frontend breaks any .map() there. The repository already does this on the read path, but
		// keep the same guarantee here so a future Repository implementation can't regress it.
		hidden = []string{}
	}
	return PreferencesDTO{
		HiddenAreas: hidden,
		UpdatedAt:   p.UpdatedAt,
	}
}
