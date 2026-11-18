package icloudcal

import (
	"testing"
	"time"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

func strptr(s string) *string { return &s }

func day(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad date %q: %v", s, err)
	}
	return parsed
}

func TestTranslator_TimedRoundTrip(t *testing.T) {
	loc := civildate.Location()
	in := EventData{
		Title:     "Reunião",
		AllDay:    false,
		StartDate: day(t, "2026-09-20"),
		StartTime: strptr("14:30"),
		EndTime:   strptr("15:30"),
	}
	cal := toICal("uid-1", in, loc, time.Now())
	out, uid, err := fromICal(cal, loc)
	if err != nil {
		t.Fatalf("fromICal: %v", err)
	}
	if uid != "uid-1" {
		t.Fatalf("uid = %q", uid)
	}
	if out.AllDay {
		t.Fatal("expected a timed event")
	}
	if out.StartTime == nil || *out.StartTime != "14:30" {
		t.Fatalf("start = %v, want 14:30", out.StartTime)
	}
	if out.EndTime == nil || *out.EndTime != "15:30" {
		t.Fatalf("end = %v, want 15:30", out.EndTime)
	}
	if !out.StartDate.Equal(in.StartDate) {
		t.Fatalf("start date = %v, want %v", out.StartDate, in.StartDate)
	}
}

func TestTranslator_AllDaySingleDay(t *testing.T) {
	loc := civildate.Location()
	in := EventData{Title: "Feriado", AllDay: true, StartDate: day(t, "2026-09-20")}
	cal := toICal("uid-2", in, loc, time.Now())
	out, _, err := fromICal(cal, loc)
	if err != nil {
		t.Fatalf("fromICal: %v", err)
	}
	if !out.AllDay {
		t.Fatal("expected all-day")
	}
	if out.EndDate != nil {
		t.Fatalf("single-day event should have no end date, got %v", out.EndDate)
	}
}

func TestTranslator_AllDayMultiDayInclusive(t *testing.T) {
	loc := civildate.Location()
	end := day(t, "2026-09-22")
	in := EventData{Title: "Viagem", AllDay: true, StartDate: day(t, "2026-09-20"), EndDate: &end}
	cal := toICal("uid-3", in, loc, time.Now())
	out, _, err := fromICal(cal, loc)
	if err != nil {
		t.Fatalf("fromICal: %v", err)
	}
	if out.EndDate == nil || !out.EndDate.Equal(end) {
		t.Fatalf("end = %v, want %v (DTEND is exclusive on the wire, inclusive in the domain)", out.EndDate, end)
	}
}

func TestTranslator_TimedWithoutEnd(t *testing.T) {
	loc := civildate.Location()
	in := EventData{Title: "Lembrete", StartDate: day(t, "2026-09-20"), StartTime: strptr("09:00")}
	cal := toICal("uid-4", in, loc, time.Now())
	out, _, err := fromICal(cal, loc)
	if err != nil {
		t.Fatalf("fromICal: %v", err)
	}
	if out.EndTime != nil {
		t.Fatalf("expected no end time, got %v", out.EndTime)
	}
	if out.StartTime == nil || *out.StartTime != "09:00" {
		t.Fatalf("start = %v, want 09:00", out.StartTime)
	}
}
