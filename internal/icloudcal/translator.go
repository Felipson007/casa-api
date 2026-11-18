package icloudcal

import (
	"errors"
	"fmt"
	"time"

	"github.com/emersion/go-ical"
)

// toICal renders an EventData as a single-VEVENT VCALENDAR. Timed events are written as UTC
// instants (the wall clock is interpreted in loc and converted), which every client displays back
// in its own local zone — for this one-household, one-timezone app that round-trips exactly.
// All-day events use VALUE=DATE, with DTEND exclusive per RFC 5545 (omitted for a single day).
func toICal(uid string, d EventData, loc *time.Location, now time.Time) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Atlas//Pandora Finance//PT-BR")

	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, uid)
	ev.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	ev.Props.SetText(ical.PropSummary, d.Title)
	if d.Description != nil && *d.Description != "" {
		ev.Props.SetText(ical.PropDescription, *d.Description)
	}
	if d.Location != nil && *d.Location != "" {
		ev.Props.SetText(ical.PropLocation, *d.Location)
	}

	if d.AllDay {
		ev.Props.SetDate(ical.PropDateTimeStart, d.StartDate)
		// DTEND is always emitted: iCloud answers 404 on a PUT whose VEVENT has no DTEND (a known
		// server quirk — see pimutils/vdirsyncer#1200), even though RFC 5545 allows omitting it for
		// a single all-day occurrence. DTEND is exclusive, so a single day ends the next day.
		end := d.StartDate.AddDate(0, 0, 1)
		if d.EndDate != nil {
			end = d.EndDate.AddDate(0, 0, 1)
		}
		ev.Props.SetDate(ical.PropDateTimeEnd, end)
	} else {
		start := combine(d.StartDate, d.StartTime, loc)
		ev.Props.SetDateTime(ical.PropDateTimeStart, start.UTC())
		endDay := d.StartDate
		if d.EndDate != nil {
			endDay = *d.EndDate
		}
		// Same iCloud quirk for timed events: always carry a DTEND (zero-length when the user
		// gave no end time).
		end := start
		if d.EndTime != nil {
			end = combine(endDay, d.EndTime, loc)
		}
		ev.Props.SetDateTime(ical.PropDateTimeEnd, end.UTC())
	}

	cal.Children = append(cal.Children, ev.Component)
	return cal
}

// fromICal parses the first VEVENT into EventData, returning its UID. loc is used both to render
// timed instants back to the household's wall clock and to interpret DATE values.
func fromICal(cal *ical.Calendar, loc *time.Location) (EventData, string, error) {
	if cal == nil {
		return EventData{}, "", errors.New("icloudcal: empty calendar object")
	}
	var ev *ical.Event
	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			ev = &ical.Event{Component: comp}
			break
		}
	}
	if ev == nil {
		return EventData{}, "", errors.New("icloudcal: no VEVENT in calendar object")
	}

	uid, _ := ev.Props.Text(ical.PropUID)
	title, _ := ev.Props.Text(ical.PropSummary)
	description, _ := ev.Props.Text(ical.PropDescription)
	location, _ := ev.Props.Text(ical.PropLocation)

	startProp := ev.Props.Get(ical.PropDateTimeStart)
	if startProp == nil {
		return EventData{}, "", errors.New("icloudcal: VEVENT without DTSTART")
	}
	allDay := isDateValue(startProp)

	start, err := startProp.DateTime(loc)
	if err != nil {
		return EventData{}, "", fmt.Errorf("icloudcal: parsing DTSTART: %w", err)
	}

	d := EventData{Title: title, AllDay: allDay}
	if description != "" {
		d.Description = &description
	}
	if location != "" {
		d.Location = &location
	}

	if allDay {
		d.StartDate = dayOnly(start)
		if endProp := ev.Props.Get(ical.PropDateTimeEnd); endProp != nil {
			if end, err := endProp.DateTime(loc); err == nil {
				inclusive := dayOnly(end).AddDate(0, 0, -1)
				if !sameDay(inclusive, d.StartDate) {
					d.EndDate = &inclusive
				}
			}
		}
		return d, uid, nil
	}

	local := start.In(loc)
	d.StartDate = dayOnly(local)
	startTime := local.Format("15:04")
	d.StartTime = &startTime
	if endProp := ev.Props.Get(ical.PropDateTimeEnd); endProp != nil {
		if end, err := endProp.DateTime(loc); err == nil {
			endLocal := end.In(loc)
			endTime := endLocal.Format("15:04")
			// A zero-length DTEND (what we write when the user gave no end time) is treated as "no
			// end", so it round-trips back to the same event instead of gaining a fake end.
			if !sameDay(endLocal, local) || endTime != startTime {
				if !sameDay(endLocal, local) {
					endDay := dayOnly(endLocal)
					d.EndDate = &endDay
				}
				d.EndTime = &endTime
			}
		}
	}
	return d, uid, nil
}

// isDateValue reports whether a DTSTART/DTEND property is an all-day DATE. The value type may be
// explicit (VALUE=DATE) or implicit — a bare 8-digit value is a date.
func isDateValue(prop *ical.Prop) bool {
	if prop.ValueType() == ical.ValueDate {
		return true
	}
	return prop.ValueType() == ical.ValueDefault && len(prop.Value) == len("20060102")
}

// combine builds a wall-clock time from a civil day and an "HH:MM" string in loc.
func combine(day time.Time, hhmm *string, loc *time.Location) time.Time {
	hour, minute := 0, 0
	if hhmm != nil {
		if parsed, err := time.Parse("15:04", *hhmm); err == nil {
			hour, minute = parsed.Hour(), parsed.Minute()
		}
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
}

func dayOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}
