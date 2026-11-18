package calendarsync

import (
	"testing"
	"time"

	"github.com/atlas/atlas-go/internal/calendar"
)

func TestRecentlyFailed(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

	t.Run("never failed", func(t *testing.T) {
		if recentlyFailed(calendar.Event{}, now) {
			t.Fatal("expected false when SyncFailedAt is nil")
		}
	})

	t.Run("failed just now, still within the gap", func(t *testing.T) {
		failedAt := now.Add(-time.Minute)
		if !recentlyFailed(calendar.Event{SyncFailedAt: &failedAt}, now) {
			t.Fatal("expected true right after a failure")
		}
	})

	t.Run("failed long ago, past the gap", func(t *testing.T) {
		failedAt := now.Add(-2 * pushRetryGap)
		if recentlyFailed(calendar.Event{SyncFailedAt: &failedAt}, now) {
			t.Fatal("expected false once pushRetryGap has elapsed")
		}
	})
}
