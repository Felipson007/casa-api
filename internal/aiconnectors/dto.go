package aiconnectors

// ConnectorDTO is the client-facing shape — it never includes the API key, only whether one is set.
type ConnectorDTO struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	Label           string `json:"label"`
	BaseURL         string `json:"baseUrl"`
	ChatModel       string `json:"chatModel"`
	ExtractionModel string `json:"extractionModel"`
	Priority        int    `json:"priority"`
	Enabled         bool   `json:"enabled"`
	HasAPIKey       bool   `json:"hasApiKey"`
}

func toDTO(c Connector) ConnectorDTO {
	return ConnectorDTO{
		ID:              c.ID.String(),
		Provider:        string(c.Provider),
		Label:           c.Label,
		BaseURL:         c.BaseURL,
		ChatModel:       c.ChatModel,
		ExtractionModel: c.ExtractionModel,
		Priority:        c.Priority,
		Enabled:         c.Enabled,
		HasAPIKey:       c.HasAPIKey(),
	}
}

func toDTOs(cs []Connector) []ConnectorDTO {
	out := make([]ConnectorDTO, 0, len(cs))
	for _, c := range cs {
		out = append(out, toDTO(c))
	}
	return out
}

// UpsertRequest is the request body for create/update. APIKey is plaintext in transit only; on
// update, empty means "keep the stored key".
type UpsertRequest struct {
	Provider        string `json:"provider" validate:"required"`
	Label           string `json:"label"`
	BaseURL         string `json:"baseUrl"`
	APIKey          string `json:"apiKey"`
	ChatModel       string `json:"chatModel"`
	ExtractionModel string `json:"extractionModel"`
	Priority        int    `json:"priority"`
	Enabled         bool   `json:"enabled"`
}

func (r UpsertRequest) toInput() UpsertInput {
	return UpsertInput{
		Provider:        Provider(r.Provider),
		Label:           r.Label,
		BaseURL:         r.BaseURL,
		APIKey:          r.APIKey,
		ChatModel:       r.ChatModel,
		ExtractionModel: r.ExtractionModel,
		Priority:        r.Priority,
		Enabled:         r.Enabled,
	}
}
