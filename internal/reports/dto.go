package reports

import (
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/jobs"
)

// JobDTO is the JSON shape returned by every endpoint in this module — mirrors JobDto exactly
// (tenantId/payload/retries are deliberately not exposed, same as the Java record).
type JobDTO struct {
	ID        uuid.UUID      `json:"id"`
	Type      string         `json:"type"`
	Status    string         `json:"status"`
	CreatedAt time.Time      `json:"createdAt"`
	Result    map[string]any `json:"result,omitempty"`
}

func toJobDTO(j jobs.Job) JobDTO {
	return JobDTO{ID: j.ID, Type: j.Type, Status: string(j.Status), CreatedAt: j.CreatedAt, Result: j.Result}
}

// DownloadedReport is a completed job's file bytes plus enough metadata to serve it back with a
// correct Content-Type/Content-Disposition — mirrors JobController.download's per-type filename/
// MIME logic.
type DownloadedReport struct {
	Content     []byte
	ContentType string
	Filename    string
}
