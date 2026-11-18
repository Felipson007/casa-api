package googlecal

import (
	"testing"
	"time"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

func d(y int, m time.Month, day int) time.Time {
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}

func strptr(s string) *string { return &s }

func TestFromData_AllDaySingleDay(t *testing.T) {
	body := fromData(EventData{Title: "Feriado", AllDay: true, StartDate: d(2026, 9, 25)})
	start := body["start"].(map[string]string)
	end := body["end"].(map[string]string)
	if start["date"] != "2026-09-25" {
		t.Fatalf("start.date = %q", start["date"])
	}
	// Google's all-day end is exclusive: a single day ends the next day.
	if end["date"] != "2026-09-26" {
		t.Fatalf("end.date = %q, want 2026-09-26", end["date"])
	}
}

func TestFromData_TimedWithoutEndGetsAnHour(t *testing.T) {
	body := fromData(EventData{Title: "Reunião", StartDate: d(2026, 9, 25), StartTime: strptr("14:00")})
	start := body["start"].(map[string]string)["dateTime"]
	end := body["end"].(map[string]string)["dateTime"]
	s, _ := time.Parse(time.RFC3339, start)
	e, _ := time.Parse(time.RFC3339, end)
	if e.Sub(s) != time.Hour {
		t.Fatalf("expected a 1h default duration, got %v (%s -> %s)", e.Sub(s), start, end)
	}
}

func TestFromGoogle_AllDayMultiDayInclusive(t *testing.T) {
	got, ok := fromGoogle(gEvent{
		Summary: "Viagem",
		Start:   gDate{Date: "2026-09-20"},
		End:     gDate{Date: "2026-09-23"}, // exclusive
	})
	if !ok {
		t.Fatal("fromGoogle returned !ok")
	}
	if !got.AllDay {
		t.Fatal("expected all-day")
	}
	if got.EndDate == nil || !got.EndDate.Equal(d(2026, 9, 22)) {
		t.Fatalf("end = %v, want 2026-09-22 (inclusive)", got.EndDate)
	}
}

func TestFromGoogle_Timed(t *testing.T) {
	loc := civildate.Location()
	start := time.Date(2026, 9, 25, 17, 0, 0, 0, loc)
	end := time.Date(2026, 9, 25, 18, 0, 0, 0, loc)
	got, ok := fromGoogle(gEvent{
		Summary: "Consulta",
		Start:   gDate{DateTime: start.UTC().Format(time.RFC3339)},
		End:     gDate{DateTime: end.UTC().Format(time.RFC3339)},
	})
	if !ok {
		t.Fatal("fromGoogle returned !ok")
	}
	if got.AllDay || got.StartTime == nil || *got.StartTime != "17:00" {
		t.Fatalf("start = %v (allDay=%v)", got.StartTime, got.AllDay)
	}
	if got.EndTime == nil || *got.EndTime != "18:00" {
		t.Fatalf("end = %v", got.EndTime)
	}
}
