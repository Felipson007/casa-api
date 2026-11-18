// Package civildate is a date-only JSON value — no time-of-day, no timezone.
//
// The bug this exists to fix: a DTO field typed plain time.Time marshals via encoding/json's
// default RFC3339 format, e.g. "2026-08-05T00:00:00Z", even when the underlying column is a SQL
// DATE with no time component at all. A browser in a negative UTC offset (Brazil, UTC-3) parses
// that instant and formats it in local time — "2026-08-05T00:00:00Z" becomes 2026-08-04 21:00
// local, and any dd/MM/yyyy-only formatter (date-fns' format, which uses the system timezone)
// renders the day as one less than what's actually stored. Every due date, contract date, birth
// date, etc. across the app was affected identically, since they all went through the same
// default time.Time marshaling. A civildate.Date instead always marshals as bare "2026-08-05" —
// no instant, no timezone to misinterpret.
package civildate

import (
	"database/sql/driver"
	"fmt"
	"os"
	"sync"
	"time"

	// Embeds the IANA tzdata database in the binary so time.LoadLocation works even when deployed
	// on a minimal base image with no system tzdata (e.g. a bare Alpine/scratch container) — Today
	// below must never silently fall back to UTC just because the OS image is missing this file.
	_ "time/tzdata"
)

const layout = "2006-01-02"

const defaultTimezone = "America/Sao_Paulo"

var (
	locationOnce sync.Once
	location     *time.Location
)

// Location resolves the application's civil-date timezone from APP_TIMEZONE (default
// America/Sao_Paulo — this app has one household, one timezone, never per-request/per-tenant).
// Resolved once and cached: every "what day is it" decision in the app (loans.Vigentes, the
// overdue/due-soon maintenance sweep, the recurring-interest rollover) must agree on the same
// civil day, or two code paths can disagree about whether "today" has rolled over yet — the exact
// bug this function exists to close (see its doc comment on Today).
func Location() *time.Location {
	locationOnce.Do(func() {
		name := os.Getenv("APP_TIMEZONE")
		if name == "" {
			name = defaultTimezone
		}
		loc, err := time.LoadLocation(name)
		if err != nil {
			// A misconfigured APP_TIMEZONE must not silently degrade every date comparison in the
			// app to UTC — fall back to the household's real default instead of time.UTC.
			loc, err = time.LoadLocation(defaultTimezone)
			if err != nil {
				loc = time.UTC
			}
		}
		location = loc
	})
	return location
}

// Today returns today's civil date in Location() — the fix for a real bug (see the loans package's
// vigentes.go/maintenance.go): computing "today" via a bare time.Now() and comparing it against a
// midnight-normalized DueDate has two failure modes at once. First, a container running in UTC
// rolls over to the next civil day hours before Brazil does (America/Sao_Paulo is UTC-3), so any
// installment due "tomorrow" from the household's perspective was already being treated as due
// "today" once the UTC clock ticked past 21:00 BRT. Second, even with the right timezone, comparing
// a midnight DueDate against a today WITH a time-of-day component (time.Now(), not midnight) makes
// an installment due today register as "before now" from 00:00:01 onward — one day early — instead
// of only becoming overdue starting the day after it's due. Today() normalizes to midnight in the
// correct zone specifically so DueDate.Before(Today()) means what it says: strictly a past day.
func Today() Date {
	now := time.Now().In(Location())
	return Date(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC))
}

// Date wraps time.Time for the sole purpose of JSON/DB marshaling as a bare calendar date. Never
// used for arithmetic directly — the domain layer keeps its own time.Time fields (with real
// AddDate/Before/After logic) and converts at the DTO boundary via Of/Time.
type Date time.Time

// Of converts a domain time.Time into a Date for a DTO field. The time-of-day/zone components are
// discarded on marshal regardless of what's in t, so callers don't need to normalize first.
func Of(t time.Time) Date {
	return Date(t)
}

// Ptr returns a pointer to d — a small fluent convenience for building a *Date DTO field inline
// from a value, e.g. civildate.Of(newest.RecordDate).Ptr(), without an intermediate variable.
// Pointer receiver so it's also safe (returns nil back) if called through a nil *Date.
func (d *Date) Ptr() *Date {
	return d
}

// OfPtr is Of for the common *time.Time (nullable date) case.
func OfPtr(t *time.Time) *Date {
	if t == nil {
		return nil
	}
	d := Of(*t)
	return &d
}

// Time converts back to time.Time for the rare case a caller needs it (e.g. re-deriving a domain
// value from a decoded request).
func (d Date) Time() time.Time {
	return time.Time(d)
}

func (d Date) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Time(d).Format(layout) + `"`), nil
}

func (d *Date) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		return nil
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return fmt.Errorf("civildate: %q is not a JSON string", s)
	}
	s = s[1 : len(s)-1]
	// Accept a full RFC3339 timestamp too (defensive: a client that still sends one shouldn't hard
	// fail), truncating to its date component — but the canonical, expected shape is the bare date.
	if t, err := time.Parse(layout, s); err == nil {
		*d = Date(t)
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fmt.Errorf("civildate: parsing %q: %w", s, err)
	}
	*d = Date(t)
	return nil
}

// Scan/Value implement database/sql's Scanner/Valuer so a Date can round-trip through pgx directly
// if a repository ever binds it as a query parameter — not required for the read-only DTO mapping
// this package exists for today, but kept consistent with the type's other interfaces.
func (d *Date) Scan(value any) error {
	t, ok := value.(time.Time)
	if !ok {
		return fmt.Errorf("civildate: cannot scan %T into Date", value)
	}
	*d = Date(t)
	return nil
}

func (d Date) Value() (driver.Value, error) {
	return time.Time(d), nil
}
