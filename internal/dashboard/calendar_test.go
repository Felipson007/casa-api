package dashboard

import (
	"testing"
	"time"
)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad test date %q: %v", s, err)
	}
	return parsed
}

// TestYearlyOccurrences covers what made birthdays absent from the calendar: a birth date is a
// seed, not an occurrence. Nothing was projecting it onto the year being displayed.
func TestYearlyOccurrences(t *testing.T) {
	born := day(t, "1990-03-14")

	// A range inside one year yields that year's birthday.
	got := yearlyOccurrences(born, day(t, "2026-03-01"), day(t, "2026-03-31"))
	if len(got) != 1 || !got[0].Equal(day(t, "2026-03-14")) {
		t.Fatalf("expected 2026-03-14, got %v", got)
	}

	// A range that misses the day yields nothing.
	if got := yearlyOccurrences(born, day(t, "2026-04-01"), day(t, "2026-04-30")); len(got) != 0 {
		t.Fatalf("expected no occurrence in April, got %v", got)
	}

	// A multi-year range yields one per year.
	got = yearlyOccurrences(born, day(t, "2026-01-01"), day(t, "2028-12-31"))
	if len(got) != 3 {
		t.Fatalf("expected one birthday per year over three years, got %d: %v", len(got), got)
	}

	// Never before the person existed.
	if got := yearlyOccurrences(born, day(t, "1985-01-01"), day(t, "1989-12-31")); len(got) != 0 {
		t.Fatalf("expected no birthdays before the birth year, got %v", got)
	}
}

// A 29 February birthday must still appear in common years — silently skipping it three years out
// of four is exactly the kind of quiet omission this calendar already suffered from.
func TestYearlyOccurrences_LeapDay(t *testing.T) {
	born := day(t, "2000-02-29")

	got := yearlyOccurrences(born, day(t, "2026-02-01"), day(t, "2026-02-28"))
	if len(got) != 1 || !got[0].Equal(day(t, "2026-02-28")) {
		t.Fatalf("expected the 29 Feb birthday on 2026-02-28, got %v", got)
	}

	got = yearlyOccurrences(born, day(t, "2028-02-01"), day(t, "2028-02-29"))
	if len(got) != 1 || !got[0].Equal(day(t, "2028-02-29")) {
		t.Fatalf("expected 2028-02-29 in a leap year, got %v", got)
	}
}

func TestBirthdayDetail(t *testing.T) {
	if got := birthdayDetail(1); got != "Faz 1 ano" {
		t.Errorf("singular: got %q", got)
	}
	if got := birthdayDetail(36); got != "Faz 36 anos" {
		t.Errorf("plural: got %q", got)
	}
	if got := birthdayDetail(0); got != "Aniversário" {
		t.Errorf("age unknown/zero: got %q", got)
	}
}

// An instalment is synced as "Juros - <client>" with the amounts in the note; other derived events
// keep their own title/details.
func TestRemoteTitleAndDetail(t *testing.T) {
	principal, interest := "1000.00", "150.00"
	title, detail := remoteTitleAndDetail(CalendarEventDTO{
		Type: CalendarEventInstallment, Title: "Cristina",
		LoanPrincipal: &principal, Amount: &interest,
	})
	if title != "Juros - Cristina" {
		t.Fatalf("instalment title = %q", title)
	}
	if detail != "Valor emprestado: 1000, Juros: 150" {
		t.Fatalf("instalment detail = %q", detail)
	}

	title, detail = remoteTitleAndDetail(CalendarEventDTO{
		Type: CalendarEventBirthday, Title: "Ana", Detail: "Faz 30 anos",
	})
	if title != "Ana" || detail != "Faz 30 anos" {
		t.Fatalf("birthday: title=%q detail=%q", title, detail)
	}
}
