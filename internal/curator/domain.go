// Package curator implements the daily, bounded AI review of Atlas data.
package curator

import "time"

type Insight struct {
	Type        string    `json:"type"`
	Severity    string    `json:"severity"`
	Title       string    `json:"title"`
	Message     string    `json:"message"`
	GeneratedAt time.Time `json:"generatedAt"`
}

type Report struct {
	Skipped      bool
	Reclassified int
	Insights     int
}
