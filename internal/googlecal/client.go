package googlecal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

const googleAPIBase = "https://www.googleapis.com/calendar/v3"

// GoogleAPI is the real Client, backed by the Google Calendar REST API.
type GoogleAPI struct {
	hc         *http.Client
	calendarID string
}

// NewGoogleAPI builds an authenticated client from the stored refresh token. calendarID is the
// selected calendar the sync works on.
func NewGoogleAPI(ctx context.Context, cfg *oauth2.Config, refreshToken, calendarID string) (*GoogleAPI, error) {
	if cfg == nil {
		return nil, fmt.Errorf("googlecal: google oauth is not configured")
	}
	token := &oauth2.Token{RefreshToken: refreshToken}
	hc := oauth2.NewClient(ctx, cfg.TokenSource(ctx, token))
	return &GoogleAPI{hc: hc, calendarID: calendarID}, nil
}

func (g *GoogleAPI) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	u := googleAPIBase + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<12))
		return fmt.Errorf("googlecal: %s %s: %d %s", method, path, resp.StatusCode, string(bytes.TrimSpace(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (g *GoogleAPI) ListCalendars(ctx context.Context) ([]RemoteCalendar, error) {
	var res struct {
		Items []struct {
			ID         string `json:"id"`
			Summary    string `json:"summary"`
			AccessRole string `json:"accessRole"`
		} `json:"items"`
	}
	if err := g.do(ctx, http.MethodGet, "/users/me/calendarList", nil, nil, &res); err != nil {
		return nil, err
	}
	out := make([]RemoteCalendar, 0, len(res.Items))
	for _, it := range res.Items {
		// Only writable calendars can host the two-way sync; read-only ones would fail every PUT.
		if it.AccessRole != "owner" && it.AccessRole != "writer" {
			continue
		}
		out = append(out, RemoteCalendar{Path: it.ID, Name: it.Summary})
	}
	return out, nil
}

func (g *GoogleAPI) ListEvents(ctx context.Context, calendarID string, from, to time.Time) ([]RemoteEvent, error) {
	if calendarID == "" {
		calendarID = g.calendarID
	}
	q := url.Values{}
	q.Set("timeMin", from.UTC().Format(time.RFC3339))
	q.Set("timeMax", to.UTC().Format(time.RFC3339))
	q.Set("singleEvents", "true")
	q.Set("showDeleted", "false")
	q.Set("maxResults", "2500")
	var res struct {
		Items []gEvent `json:"items"`
	}
	path := "/calendars/" + url.PathEscape(calendarID) + "/events"
	if err := g.do(ctx, http.MethodGet, path, q, nil, &res); err != nil {
		return nil, err
	}
	out := make([]RemoteEvent, 0, len(res.Items))
	for _, e := range res.Items {
		if e.Status == "cancelled" {
			continue
		}
		out = append(out, toRemote(e))
	}
	return out, nil
}

func (g *GoogleAPI) PutEvent(ctx context.Context, href, uid string, data EventData) (RemoteEvent, error) {
	body := fromData(data)
	path := "/calendars/" + url.PathEscape(g.calendarID) + "/events"
	method := http.MethodPost
	if href != "" {
		method = http.MethodPatch
		path += "/" + url.PathEscape(href)
	}
	var stored gEvent
	if err := g.do(ctx, method, path, nil, body, &stored); err != nil {
		return RemoteEvent{}, err
	}
	return toRemote(stored), nil
}

func (g *GoogleAPI) DeleteEvent(ctx context.Context, href string) error {
	path := "/calendars/" + url.PathEscape(g.calendarID) + "/events/" + url.PathEscape(href)
	return g.do(ctx, http.MethodDelete, path, nil, nil, nil)
}

// --- translation -----------------------------------------------------------------------------

type gDate struct {
	Date     string `json:"date,omitempty"`
	DateTime string `json:"dateTime,omitempty"`
}

type gEvent struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	Summary     string    `json:"summary"`
	Description string    `json:"description"`
	Location    string    `json:"location"`
	Start       gDate     `json:"start"`
	End         gDate     `json:"end"`
	Updated     time.Time `json:"updated"`
	ETag        string    `json:"etag"`
}

func toRemote(e gEvent) RemoteEvent {
	data, _ := fromGoogle(e)
	return RemoteEvent{Href: e.ID, UID: e.ID, ETag: e.ETag, Modified: e.Updated, Data: data}
}

func fromData(d EventData) map[string]any {
	body := map[string]any{"summary": d.Title}
	if d.Description != nil && *d.Description != "" {
		body["description"] = *d.Description
	}
	if d.Location != nil && *d.Location != "" {
		body["location"] = *d.Location
	}
	if d.AllDay {
		start := d.StartDate.Format("2006-01-02")
		end := d.StartDate.AddDate(0, 0, 1)
		if d.EndDate != nil {
			end = d.EndDate.AddDate(0, 0, 1)
		}
		body["start"] = map[string]string{"date": start}
		body["end"] = map[string]string{"date": end.Format("2006-01-02")}
		return body
	}
	start := combine(d.StartDate, d.StartTime)
	end := start
	if d.EndTime != nil {
		day := d.StartDate
		if d.EndDate != nil {
			day = *d.EndDate
		}
		end = combine(day, d.EndTime)
	}
	// Google requires end on/after start; a zero-length event is allowed but a 1h default is less
	// surprising in the Google UI when the user gave no end.
	if !end.After(start) {
		end = start.Add(time.Hour)
	}
	body["start"] = map[string]string{"dateTime": start.UTC().Format(time.RFC3339)}
	body["end"] = map[string]string{"dateTime": end.UTC().Format(time.RFC3339)}
	return body
}

func fromGoogle(e gEvent) (EventData, bool) {
	d := EventData{Title: e.Summary}
	if e.Description != "" {
		d.Description = &e.Description
	}
	if e.Location != "" {
		d.Location = &e.Location
	}
	loc := civildate.Location()
	switch {
	case e.Start.Date != "":
		d.AllDay = true
		start, err := time.Parse("2006-01-02", e.Start.Date)
		if err != nil {
			return EventData{}, false
		}
		d.StartDate = start
		if e.End.Date != "" {
			if end, err := time.Parse("2006-01-02", e.End.Date); err == nil {
				inclusive := end.AddDate(0, 0, -1)
				if !sameDay(inclusive, start) {
					d.EndDate = &inclusive
				}
			}
		}
	case e.Start.DateTime != "":
		start, err := time.Parse(time.RFC3339, e.Start.DateTime)
		if err != nil {
			return EventData{}, false
		}
		local := start.In(loc)
		d.StartDate = dayOnly(local)
		st := local.Format("15:04")
		d.StartTime = &st
		if e.End.DateTime != "" {
			if end, err := time.Parse(time.RFC3339, e.End.DateTime); err == nil {
				el := end.In(loc)
				et := el.Format("15:04")
				if !sameDay(el, local) {
					ed := dayOnly(el)
					d.EndDate = &ed
				}
				if et != st || !sameDay(el, local) {
					d.EndTime = &et
				}
			}
		}
	default:
		return EventData{}, false
	}
	return d, true
}

func combine(day time.Time, hhmm *string) time.Time {
	hour, minute := 0, 0
	if hhmm != nil {
		if parsed, err := time.Parse("15:04", *hhmm); err == nil {
			hour, minute = parsed.Hour(), parsed.Minute()
		}
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, civildate.Location())
}

func dayOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}
