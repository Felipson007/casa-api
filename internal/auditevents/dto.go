package auditevents

// EventDTO is the JSON shape of one audit event returned to clients. Long fields (detalhe) are
// only sent by the detail endpoint / list — the UI truncates for the toast summary.
type EventDTO struct {
	ID         string         `json:"id"`
	Tipo       string         `json:"tipo"`
	Severidade string         `json:"severidade"`
	UsuarioID  *string        `json:"usuarioId,omitempty"`
	Usuario    string         `json:"usuario,omitempty"`
	IP         string         `json:"ip,omitempty"`
	UserAgent  string         `json:"userAgent,omitempty"`
	Metodo     string         `json:"metodo,omitempty"`
	Caminho    string         `json:"caminho,omitempty"`
	StatusCode *int           `json:"statusCode,omitempty"`
	RequestID  string         `json:"requestId,omitempty"`
	Mensagem   string         `json:"mensagem,omitempty"`
	Detalhe    string         `json:"detalhe,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	DuracaoMs  *int           `json:"duracaoMs,omitempty"`
	CriadoEm   string         `json:"criadoEm"`
}

// ListResponse is the paginated list payload (not Spring Data's Page — this is a new endpoint).
type ListResponse struct {
	Items        []EventDTO `json:"items"`
	Total        int        `json:"total"`
	Pagina       int        `json:"pagina"`
	Tamanho      int        `json:"tamanho"`
	TotalPaginas int        `json:"totalPaginas"`
}

// DayCountDTO is one point of the errors-over-time chart.
type DayCountDTO struct {
	Dia   string `json:"dia"`
	Total int    `json:"total"`
	Erros int    `json:"erros"`
}

// TypeCountDTO is one slice of the events-by-type chart.
type TypeCountDTO struct {
	Tipo  string `json:"tipo"`
	Total int    `json:"total"`
}

// StatsResponse backs the Auditoria dashboard cards, charts and recent-logins list.
type StatsResponse struct {
	JanelaDias     int            `json:"janelaDias"`
	Total          int            `json:"total"`
	Erros          int            `json:"erros"`
	Alertas        int            `json:"alertas"`
	Logins         int            `json:"logins"`
	LoginsFalhos   int            `json:"loginsFalhos"`
	PorDia         []DayCountDTO  `json:"porDia"`
	PorTipo        []TypeCountDTO `json:"porTipo"`
	LoginsRecentes []EventDTO     `json:"loginsRecentes"`
}

// ClientReportRequest is the body the frontend posts when it catches an uncaught exception.
type ClientReportRequest struct {
	Mensagem string `json:"mensagem" validate:"required"`
	Stack    string `json:"stack"`
	Caminho  string `json:"caminho"`
}
