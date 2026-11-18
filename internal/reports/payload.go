package reports

// payloadInt reads an int out of a job payload map — payloads round-trip through jsonb, so a
// value written as a Go int comes back as a float64 once decoded into map[string]any (the standard
// encoding/json behavior for untyped numbers); this normalizes either shape.
func payloadInt(payload map[string]any, key string) int {
	switch v := payload[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}
