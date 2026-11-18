package dashboard

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/atlas/atlas-go/internal/calendar"
	"github.com/atlas/atlas-go/internal/loans"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// logSourceFailure records a single calendar source failing without taking the calendar with it —
// see CalendarEvents on why partial results beat an error page here.
func logSourceFailure(source string, err error) {
	log.Printf("dashboard.CalendarEvents: %s unavailable: %v", source, err)
}

// The calendar used to be assembled in the frontend from whatever the dashboard summary happened
// to carry, which meant two sources and one month: loan installments from `vigentesMesAtual` (the
// current month only, so next month's instalment was never on the calendar) and documents with an
// expiration date. Birthdays were absent entirely. Building it here instead gives every source the
// same arbitrary date range and puts the yearly-recurrence rule in one place.

// CalendarEventType is the closed set of things that can land on a day.
type CalendarEventType string

const (
	CalendarEventInstallment CalendarEventType = "INSTALLMENT" // parcela de empréstimo
	CalendarEventBirthday    CalendarEventType = "BIRTHDAY"    // aniversário de pessoa
	CalendarEventPetBirthday CalendarEventType = "PET_BIRTHDAY"
	CalendarEventDocument    CalendarEventType = "DOCUMENT" // vencimento de seguro/garantia/contrato/fiscal
	// CalendarEventManual is an event the owner typed (internal/calendar) — the only type the
	// frontend may edit/delete from the calendar.
	CalendarEventManual CalendarEventType = "MANUAL"
)

// CalendarEventDTO is one dated entry. Amount is a numeric(15,2)-as-string like every other money
// value here, and nil when the event isn't about money (a birthday). Age is set only for
// birthdays — the age the person or pet turns on that date. The time and description fields are
// set only for MANUAL events; the derived sources are date-only.
type CalendarEventDTO struct {
	ID          string            `json:"id"`
	Type        CalendarEventType `json:"type"`
	Title       string            `json:"title"`
	Detail      string            `json:"detail"`
	Date        civildate.Date    `json:"date"`
	EndDate     *civildate.Date   `json:"endDate,omitempty"`
	AllDay      bool              `json:"allDay"`
	StartTime   *string           `json:"startTime,omitempty"`
	EndTime     *string           `json:"endTime,omitempty"`
	Description *string           `json:"description,omitempty"`
	Location    *string           `json:"location,omitempty"`
	Source      *string           `json:"source,omitempty"`
	Amount      *string           `json:"amount,omitempty"`
	Age         *int              `json:"age,omitempty"`
	// LoanPrincipal is set only for INSTALLMENT events — the loan's original amount, used to build
	// the note on the synced event ("Valor emprestado: ..., Juros: ...").
	LoanPrincipal *string `json:"loanPrincipal,omitempty"`
	// Overdue is only meaningful for the dated obligations (instalments, documents); a birthday or
	// a manually created event is never "late".
	Overdue bool `json:"overdue"`
}

// CalendarEvents returns everything falling between from and to inclusive. A failure in any single
// source is swallowed rather than failing the whole calendar: a calendar missing its birthdays is
// worth more than an error page, and that is exactly the degradation the old frontend version
// couldn't express.
func (s *Service) CalendarEvents(ctx context.Context, from, to time.Time) ([]CalendarEventDTO, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("dashboard: calendar range ends before it starts")
	}
	from = dayStart(from)
	to = dayStart(to)

	events := make([]CalendarEventDTO, 0, 32)
	events = append(events, s.installmentEvents(ctx, from, to)...)
	events = append(events, s.birthdayEvents(ctx, from, to)...)
	events = append(events, s.documentEvents(ctx, from, to)...)
	events = append(events, s.manualEvents(ctx, from, to)...)

	today := civildate.Today().Time()
	for i := range events {
		if events[i].Type == CalendarEventBirthday || events[i].Type == CalendarEventPetBirthday {
			continue
		}
		if events[i].Type == CalendarEventManual {
			// A manually created event is an appointment, not an obligation — never "overdue".
			events[i].Overdue = false
			continue
		}
		events[i].Overdue = events[i].Date.Time().Before(today)
	}

	sort.Slice(events, func(i, j int) bool {
		if events[i].Date.Time().Equal(events[j].Date.Time()) {
			return events[i].Title < events[j].Title
		}
		return events[i].Date.Time().Before(events[j].Date.Time())
	})
	return events, nil
}

// DerivedEvents returns only the Atlas-owned entries (instalments, birthdays, document
// expirations) for the window, in the shape the calendar sync materializes as read-only rows. The
// DTO's synthetic id is reused as the stable key.
func (s *Service) DerivedEvents(ctx context.Context, from, to time.Time) []calendar.DerivedEvent {
	events := make([]CalendarEventDTO, 0, 64)
	events = append(events, s.installmentEvents(ctx, dayStart(from), dayStart(to))...)
	events = append(events, s.birthdayEvents(ctx, dayStart(from), dayStart(to))...)
	events = append(events, s.documentEvents(ctx, dayStart(from), dayStart(to))...)

	out := make([]calendar.DerivedEvent, 0, len(events))
	for _, e := range events {
		title, detail := remoteTitleAndDetail(e)
		desc := detail
		out = append(out, calendar.DerivedEvent{
			Key:         e.ID,
			Title:       title,
			Description: &desc,
			StartDate:   e.Date.Time(),
		})
	}
	return out
}

// remoteTitleAndDetail decides what the external calendar shows. A loan instalment is titled
// "Juros - <client>" with the amounts in the note; the Atlas calendar page keeps showing the name
// (this mapping only feeds the sync).
func remoteTitleAndDetail(e CalendarEventDTO) (title, detail string) {
	if e.Type == CalendarEventInstallment {
		return "Juros - " + e.Title,
			fmt.Sprintf("Valor emprestado: %s, Juros: %s", humanMoney(e.LoanPrincipal), humanMoney(e.Amount))
	}
	return e.Title, e.Detail
}

// humanMoney renders a numeric(15,2)-as-string without the redundant ".00".
func humanMoney(v *string) string {
	if v == nil || *v == "" {
		return "-"
	}
	if strings.HasSuffix(*v, ".00") {
		return strings.TrimSuffix(*v, ".00")
	}
	return *v
}

// installmentEvents asks the loans module for every month the range touches — Vigentes already
// takes a slice of months, which is precisely what the old single-month call was throwing away.
func (s *Service) installmentEvents(ctx context.Context, from, to time.Time) []CalendarEventDTO {
	if s.loans == nil {
		return nil
	}
	var months []loans.YearMonth
	for cursor := firstOfMonth(from); !cursor.After(to); cursor = cursor.AddDate(0, 1, 0) {
		months = append(months, loans.YearMonth{Year: cursor.Year(), Month: int(cursor.Month())})
	}
	byMonth, err := s.loans.Vigentes(ctx, months)
	if err != nil {
		logSourceFailure("loan instalments", err)
		return nil
	}

	var events []CalendarEventDTO
	for _, summaries := range byMonth {
		for _, l := range summaries {
			if l.InstallmentDueDate == nil {
				continue
			}
			due := l.InstallmentDueDate.Time()
			if due.Before(from) || due.After(to) {
				continue
			}
			// Settled months have nothing left to pay; only open obligations belong on a calendar.
			if l.Status != loans.MonthStatusPending && l.Status != loans.MonthStatusOverdue {
				continue
			}
			amount := l.InterestThisMonth
			principal := l.PrincipalAmount
			events = append(events, CalendarEventDTO{
				ID:            fmt.Sprintf("installment-%s-%s", l.LoanID, due.Format("2006-01-02")),
				Type:          CalendarEventInstallment,
				Title:         l.ClientName,
				Detail:        "Parcela de empréstimo",
				Date:          civildate.Of(due),
				AllDay:        true,
				Amount:        &amount,
				LoanPrincipal: &principal,
			})
		}
	}
	return events
}

// birthdayEvents projects each birth date onto every year the range covers — a birthday is a
// recurring event, so the stored date is a seed, not an occurrence. Feb 29 falls back to Feb 28 in
// a common year rather than vanishing.
func (s *Service) birthdayEvents(ctx context.Context, from, to time.Time) []CalendarEventDTO {
	var events []CalendarEventDTO

	if s.people != nil {
		items, err := s.people.ListPeople(ctx, "")
		if err != nil {
			logSourceFailure("people birthdays", err)
		} else {
			for _, p := range items {
				if p.BirthDate == nil {
					continue
				}
				born := p.BirthDate.Time()
				for _, occurrence := range yearlyOccurrences(born, from, to) {
					age := occurrence.Year() - born.Year()
					events = append(events, CalendarEventDTO{
						ID:     fmt.Sprintf("birthday-%s-%d", p.ID, occurrence.Year()),
						Type:   CalendarEventBirthday,
						Title:  p.Name,
						Detail: birthdayDetail(age),
						Date:   civildate.Of(occurrence),
						AllDay: true,
						Age:    &age,
					})
				}
			}
		}
	}

	if s.pets != nil {
		active := true
		items, err := s.pets.List(ctx, &active)
		if err != nil {
			logSourceFailure("pet birthdays", err)
		} else {
			for _, p := range items {
				if p.BirthDate == nil {
					continue
				}
				born := p.BirthDate.Time()
				for _, occurrence := range yearlyOccurrences(born, from, to) {
					age := occurrence.Year() - born.Year()
					events = append(events, CalendarEventDTO{
						ID:     fmt.Sprintf("pet-birthday-%s-%d", p.ID, occurrence.Year()),
						Type:   CalendarEventPetBirthday,
						Title:  p.Name,
						Detail: birthdayDetail(age),
						Date:   civildate.Of(occurrence),
						AllDay: true,
						Age:    &age,
					})
				}
			}
		}
	}
	return events
}

func (s *Service) documentEvents(ctx context.Context, from, to time.Time) []CalendarEventDTO {
	if s.documents == nil {
		return nil
	}
	items, err := s.documents.List(ctx, nil, nil, nil, "")
	if err != nil {
		logSourceFailure("document expirations", err)
		return nil
	}
	var events []CalendarEventDTO
	for _, d := range items {
		if d.ExpirationDate == nil {
			continue
		}
		due := d.ExpirationDate.Time()
		if due.Before(from) || due.After(to) {
			continue
		}
		title := d.OriginalFilename
		if d.Description != nil && *d.Description != "" {
			title = *d.Description
		}
		detail := "Documento"
		if d.Institution != nil && *d.Institution != "" {
			detail = *d.Institution
		}
		events = append(events, CalendarEventDTO{
			ID:     fmt.Sprintf("document-%s", d.ID),
			Type:   CalendarEventDocument,
			Title:  title,
			Detail: detail,
			Date:   civildate.Of(due),
			AllDay: true,
			Amount: d.Amount,
		})
	}
	return events
}

// manualEvents merges the owner's own calendar entries (internal/calendar) into the same dated
// list as the derived sources. These are the only entries the frontend can edit or delete, so the
// DTO carries the raw event id (not a synthetic "source-id" string) plus its times.
func (s *Service) manualEvents(ctx context.Context, from, to time.Time) []CalendarEventDTO {
	if s.calendar == nil {
		return nil
	}
	items, err := s.calendar.ListRange(ctx, from, to)
	if err != nil {
		logSourceFailure("manual events", err)
		return nil
	}
	out := make([]CalendarEventDTO, 0, len(items))
	for _, e := range items {
		source := string(e.Source)
		out = append(out, CalendarEventDTO{
			ID:          e.ID.String(),
			Type:        CalendarEventManual,
			Title:       e.Title,
			Detail:      manualDetail(e),
			Date:        e.StartDate,
			EndDate:     e.EndDate,
			AllDay:      e.AllDay,
			StartTime:   e.StartTime,
			EndTime:     e.EndTime,
			Description: e.Description,
			Location:    e.Location,
			Source:      &source,
		})
	}
	return out
}

// manualDetail is the one-line subtitle shown under the title. Description wins; location is the
// fallback; a generic label keeps the row from looking unfinished when both are empty.
func manualDetail(e calendar.EventDTO) string {
	if e.Description != nil && *e.Description != "" {
		return *e.Description
	}
	if e.Location != nil && *e.Location != "" {
		return *e.Location
	}
	return "Evento"
}

// yearlyOccurrences returns the anniversaries of seed that fall within [from, to]. Only years at
// or after the seed's own are produced — nobody has a birthday before they were born.
func yearlyOccurrences(seed, from, to time.Time) []time.Time {
	var out []time.Time
	for year := from.Year(); year <= to.Year(); year++ {
		if year < seed.Year() {
			continue
		}
		day := seed.Day()
		// 29 February in a common year: mark it on the 28th rather than silently skipping the
		// person's birthday three years out of four.
		if seed.Month() == time.February && day == 29 && !isLeap(year) {
			day = 28
		}
		occurrence := time.Date(year, seed.Month(), day, 0, 0, 0, 0, time.UTC)
		if occurrence.Before(from) || occurrence.After(to) {
			continue
		}
		out = append(out, occurrence)
	}
	return out
}

func isLeap(year int) bool {
	return (year%4 == 0 && year%100 != 0) || year%400 == 0
}

func birthdayDetail(age int) string {
	if age <= 0 {
		return "Aniversário"
	}
	if age == 1 {
		return "Faz 1 ano"
	}
	return fmt.Sprintf("Faz %d anos", age)
}

func firstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
