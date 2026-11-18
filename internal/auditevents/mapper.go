package auditevents

// toDTO maps a stored event to its JSON shape. Times are RFC3339 with milliseconds (the frontend
// parses them with the rest of the app's date helpers).
func toDTO(ev Event) EventDTO {
	dto := EventDTO{
		ID:         ev.ID.String(),
		Tipo:       ev.Type,
		Severidade: ev.Severity,
		Usuario:    ev.UserEmail,
		IP:         ev.IP,
		UserAgent:  ev.UserAgent,
		Metodo:     ev.Method,
		Caminho:    ev.Path,
		StatusCode: ev.StatusCode,
		RequestID:  ev.RequestID,
		Mensagem:   ev.Message,
		Detalhe:    ev.Detail,
		Metadata:   ev.Metadata,
		DuracaoMs:  ev.DurationMS,
		CriadoEm:   ev.CreatedAt.Format(timeLayout),
	}
	if ev.UserID != nil {
		id := ev.UserID.String()
		dto.UsuarioID = &id
	}
	return dto
}

// timeLayout is RFC3339 with milliseconds — the shape the frontend's date parsing expects.
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

func toDTOs(events []Event) []EventDTO {
	out := make([]EventDTO, 0, len(events))
	for _, ev := range events {
		out = append(out, toDTO(ev))
	}
	return out
}

func toStatsDTO(stats Stats) StatsResponse {
	resp := StatsResponse{
		JanelaDias:     stats.WindowDays,
		Total:          stats.Total,
		Erros:          stats.Errors,
		Alertas:        stats.Warnings,
		Logins:         stats.Logins,
		LoginsFalhos:   stats.FailedLogins,
		PorDia:         make([]DayCountDTO, 0, len(stats.ByDay)),
		PorTipo:        make([]TypeCountDTO, 0, len(stats.ByType)),
		LoginsRecentes: toDTOs(stats.RecentLogins),
	}
	for _, d := range stats.ByDay {
		resp.PorDia = append(resp.PorDia, DayCountDTO{Dia: d.Day, Total: d.Total, Erros: d.Errors})
	}
	for _, t := range stats.ByType {
		resp.PorTipo = append(resp.PorTipo, TypeCountDTO{Tipo: t.Type, Total: t.Count})
	}
	return resp
}
