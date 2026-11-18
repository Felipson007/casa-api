package dashboardlayout

func toDTO(l Layout) LayoutDTO {
	return LayoutDTO{
		Layout:    l.Items,
		UpdatedAt: l.UpdatedAt,
	}
}
