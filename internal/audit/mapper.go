package audit

import (
	"encoding/json"
	"time"
)

func toDTO(a AuditLog) AuditLogDTO {
	return AuditLogDTO{
		ID:        a.ID,
		TableName: a.TableName,
		RowID:     a.RowID,
		OldData:   a.OldData,
		ChangedAt: a.ChangedAt.Format(time.RFC3339),
	}
}

func toDTOs(as []AuditLog) []AuditLogDTO {
	dtos := make([]AuditLogDTO, len(as))
	for i, a := range as {
		dtos[i] = toDTO(a)
	}
	return dtos
}

// oldDataToJSONB marshals the OldData map into the jsonb byte form the repository expects.
func oldDataToJSONB(m map[string]interface{}) ([]byte, error) {
	if m == nil {
		m = map[string]interface{}{}
	}
	return json.Marshal(m)
}
