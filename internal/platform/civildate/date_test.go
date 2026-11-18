package civildate

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDate_MarshalsAsBareDate(t *testing.T) {
	// A UTC midnight timestamp, exactly what pgx scans a SQL DATE column into — the regression this
	// type exists to fix is a downstream negative-UTC-offset client misreading this as the day before.
	d := Of(time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC))

	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if string(b) != `"2026-08-05"` {
		t.Fatalf(`expected "2026-08-05", got %s`, b)
	}
}

func TestDate_UnmarshalsBareDate(t *testing.T) {
	var d Date
	if err := json.Unmarshal([]byte(`"2026-08-05"`), &d); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got := d.Time(); !got.Equal(time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected 2026-08-05, got %v", got)
	}
}

func TestDate_UnmarshalsFullTimestampDefensively(t *testing.T) {
	var d Date
	if err := json.Unmarshal([]byte(`"2026-08-05T00:00:00Z"`), &d); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got := d.Time(); !got.Equal(time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected 2026-08-05, got %v", got)
	}
}

func TestOfPtr_NilStaysNil(t *testing.T) {
	if OfPtr(nil) != nil {
		t.Fatal("expected OfPtr(nil) to return nil")
	}
}

func TestLocation_DefaultsToSaoPaulo(t *testing.T) {
	if got := Location(); got.String() != "America/Sao_Paulo" {
		t.Fatalf("expected America/Sao_Paulo by default (no APP_TIMEZONE set in this test process), got %v", got)
	}
}

func TestToday_HasNoTimeOfDayComponent(t *testing.T) {
	got := Today().Time()
	if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 || got.Nanosecond() != 0 {
		t.Fatalf("expected Today() to be midnight-normalized, got %v", got)
	}
}

func TestToday_MatchesRealCalendarDayInLocation(t *testing.T) {
	want := time.Now().In(Location())
	got := Today().Time()
	if got.Year() != want.Year() || got.Month() != want.Month() || got.Day() != want.Day() {
		t.Fatalf("expected Today() to match the calendar day in %v (%v), got %v", Location(), want, got)
	}
}

func TestDate_PtrIsIdentityAndNilSafe(t *testing.T) {
	d := Of(time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC))
	if d.Ptr() != &d {
		t.Fatal("expected Ptr() to return the same address it was called on")
	}
	var nilDate *Date
	if nilDate.Ptr() != nil {
		t.Fatal("expected Ptr() called through a nil *Date to stay nil, not panic")
	}
}

func TestOfPtr_RoundTrips(t *testing.T) {
	tm := time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC)
	got := OfPtr(&tm)
	if got == nil || !got.Time().Equal(tm) {
		t.Fatalf("expected a Date wrapping %v, got %v", tm, got)
	}
}
