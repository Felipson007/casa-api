package icloudcal

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// defaultEndpoint is iCloud's CalDAV entry point. iCloud redirects each account to a sharded host
// (pXX-caldav.icloud.com); see discover.
const defaultEndpoint = "https://caldav.icloud.com"

// CaldavClient is the real Client, backed by go-webdav + go-ical.
type CaldavClient struct {
	cal *caldav.Client
	// hc is the authenticated HTTP client, used for the hand-rolled REPORT (see ListEvents).
	hc webdav.HTTPClient
	// endpoint is the base URL actually used (the sharded host when discovery found one).
	endpoint string
}

// NewCaldavClient discovers the account's CalDAV host and builds the client against it. go-webdav
// strips the host from the hrefs it returns (FindCalendarHomeSet etc. give paths only), so if the
// client stayed on caldav.icloud.com, reads (REPORT) would work while writes (PUT/DELETE) to the
// sharded collection would 404 — which is exactly the failure this discovery fixes.
func NewCaldavClient(ctx context.Context, appleID, appPassword string) (*CaldavClient, error) {
	endpoint := discover(ctx, appleID, appPassword)
	httpClient := webdav.HTTPClientWithBasicAuth(&http.Client{Timeout: 30 * time.Second}, appleID, appPassword)
	cal, err := caldav.NewClient(httpClient, endpoint)
	if err != nil {
		return nil, fmt.Errorf("icloudcal: building caldav client: %w", err)
	}
	return &CaldavClient{cal: cal, hc: httpClient, endpoint: endpoint}, nil
}

// Endpoint reports the base URL in use (for diagnostics/tests).
func (c *CaldavClient) Endpoint() string { return c.endpoint }

// discover walks the CalDAV discovery chain itself (current-user-principal → calendar-home-set)
// capturing the host from the absolute hrefs iCloud returns, and returns "https://<host>". It
// falls back to the default entry point when anything is missing. This is deliberately not done
// through caldav.Client: that package returns href *paths* only, losing the shard host.
//
// iCloud quirk: the current-user-principal href is relative (so it reveals no host), while the
// calendar-home-set href is absolute on the shard (https://pXX-caldav.icloud.com/...). The two
// properties are returned together, so each step reads one specific property — reading "the first
// href" would grab the relative principal and miss the shard.
func discover(ctx context.Context, appleID, appPassword string) string {
	client := &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// 1) current-user-principal at the entry point (or its redirect target).
	base, ok := redirectBase(ctx, client, defaultEndpoint, appleID, appPassword)
	if !ok {
		base = defaultEndpoint
	}
	principal, host := propfindHref(ctx, client, base, "/", principalHrefFrom, appleID, appPassword)
	if host != "" {
		return "https://" + host
	}
	if principal == "" {
		return base
	}

	// 2) calendar-home-set from the principal; its absolute href carries the shard host on iCloud.
	_, host = propfindHref(ctx, client, base, principal, homeSetHrefFrom, appleID, appPassword)
	if host != "" {
		return "https://" + host
	}
	return base
}

// redirectBase performs a GET on url following no redirects and returns the Location's base
// ("https://host") when one is present, with ok=false otherwise.
func redirectBase(ctx context.Context, client *http.Client, rawURL, appleID, appPassword string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", false
	}
	req.SetBasicAuth(appleID, appPassword)
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if u, err := url.Parse(strings.TrimSpace(resp.Header.Get("Location"))); err == nil && u.Host != "" {
			return "https://" + u.Host, true
		}
	}
	return "", false
}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:current-user-principal/><c:calendar-home-set/></d:prop></d:propfind>`

// propfindHref issues a PROPFIND (Depth: 0) at base+path and returns the href selected by extract
// plus, when the href is absolute, its host.
func propfindHref(ctx context.Context, client *http.Client, base, path string, extract func([]byte) string, appleID, appPassword string) (href, host string) {
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", base+path, strings.NewReader(propfindBody))
	if err != nil {
		return "", ""
	}
	req.SetBasicAuth(appleID, appPassword)
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", `application/xml; charset="utf-8"`)

	resp, err := client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", ""
	}
	href = extract(raw)
	if href == "" {
		return "", ""
	}
	if u, uerr := url.Parse(href); uerr == nil && u.Host != "" {
		return href, u.Host
	}
	return href, ""
}

// principalHrefFrom extracts the current-user-principal href from a multistatus body.
func principalHrefFrom(raw []byte) string {
	var ms struct {
		Responses []struct {
			Propstat []struct {
				Prop struct {
					CurrentUserPrincipal struct {
						Href string `xml:"href"`
					} `xml:"current-user-principal"`
				} `xml:"prop"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.NewDecoder(bytes.NewReader(raw)).Decode(&ms); err != nil {
		return ""
	}
	for _, r := range ms.Responses {
		for _, ps := range r.Propstat {
			if h := strings.TrimSpace(ps.Prop.CurrentUserPrincipal.Href); h != "" {
				return h
			}
		}
	}
	return ""
}

// homeSetHrefFrom extracts the calendar-home-set href from a multistatus body.
func homeSetHrefFrom(raw []byte) string {
	var ms struct {
		Responses []struct {
			Propstat []struct {
				Prop struct {
					CalendarHomeSet struct {
						Href string `xml:"href"`
					} `xml:"calendar-home-set"`
				} `xml:"prop"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.NewDecoder(bytes.NewReader(raw)).Decode(&ms); err != nil {
		return ""
	}
	for _, r := range ms.Responses {
		for _, ps := range r.Propstat {
			if h := strings.TrimSpace(ps.Prop.CalendarHomeSet.Href); h != "" {
				return h
			}
		}
	}
	return ""
}

// ListCalendars discovers the account's principal, its calendar-home-set and the collections
// underneath.
func (c *CaldavClient) ListCalendars(ctx context.Context) ([]RemoteCalendar, error) {
	principal, err := c.cal.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("icloudcal: finding principal: %w", err)
	}
	home, err := c.cal.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("icloudcal: finding calendar home: %w", err)
	}
	cals, err := c.cal.FindCalendars(ctx, home)
	if err != nil {
		return nil, fmt.Errorf("icloudcal: listing calendars: %w", err)
	}
	out := make([]RemoteCalendar, 0, len(cals))
	for _, cal := range cals {
		out = append(out, RemoteCalendar{Path: cal.Path, Name: cal.Name})
	}
	return out, nil
}

// calendarQueryBody requests the full iCalendar of every VEVENT plus its ETag and Last-Modified.
// No server-side time-range: iCloud's matching for all-day (VALUE=DATE) events is unreliable, so
// the range is applied client-side.
const calendarQueryBody = `<?xml version="1.0" encoding="utf-8"?><c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:getetag/><d:getlastmodified/><c:calendar-data/></d:prop><c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT"/></c:comp-filter></c:filter></c:calendar-query>`

// calQueryResponse is the subset of a calendar-query multistatus this needs. A response without
// calendar-data (a non-event resource) is skipped rather than failing the whole query — go-webdav's
// QueryCalendar errors on iCloud exactly because iCloud sends such a response.
type calQueryResponse struct {
	Responses []struct {
		Href     string `xml:"href"`
		Propstat []struct {
			Prop struct {
				ETag         string `xml:"getetag"`
				LastModified string `xml:"getlastmodified"`
				CalendarData string `xml:"calendar-data"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

func (c *CaldavClient) ListEvents(ctx context.Context, calendarPath string, from, to time.Time) ([]RemoteEvent, error) {
	req, err := http.NewRequestWithContext(ctx, "REPORT", c.endpoint+calendarPath, strings.NewReader(calendarQueryBody))
	if err != nil {
		return nil, fmt.Errorf("icloudcal: building calendar query: %w", err)
	}
	req.Header.Set("Content-Type", `application/xml; charset="utf-8"`)
	req.Header.Set("Depth", "1")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("icloudcal: querying calendar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("icloudcal: querying calendar: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("icloudcal: reading calendar query: %w", err)
	}
	var ms calQueryResponse
	if err := xml.NewDecoder(bytes.NewReader(raw)).Decode(&ms); err != nil {
		return nil, fmt.Errorf("icloudcal: decoding calendar query: %w", err)
	}

	fromDay, toDay := dayOnly(from), dayOnly(to)
	out := make([]RemoteEvent, 0, len(ms.Responses))
	for _, r := range ms.Responses {
		var ics, etag, lastMod string
		for _, ps := range r.Propstat {
			if ps.Prop.CalendarData != "" {
				ics = ps.Prop.CalendarData
				etag = strings.Trim(ps.Prop.ETag, `"`)
				lastMod = ps.Prop.LastModified
				break
			}
		}
		if ics == "" {
			continue
		}
		cal, err := ical.NewDecoder(strings.NewReader(ics)).Decode()
		if err != nil {
			continue
		}
		data, uid, err := fromICal(cal, civildate.Location())
		if err != nil {
			continue
		}
		end := data.StartDate
		if data.EndDate != nil {
			end = *data.EndDate
		}
		if data.StartDate.After(toDay) || end.Before(fromDay) {
			continue
		}
		event := RemoteEvent{Href: hrefPath(r.Href), UID: uid, ETag: etag, Data: data}
		if t, err := http.ParseTime(lastMod); err == nil {
			event.Modified = t
		}
		out = append(out, event)
	}
	return out, nil
}

// hrefPath turns a response href (which iCloud sends as an absolute URL on the shard) into the path
// the PUT/DELETE calls expect.
func hrefPath(href string) string {
	href = strings.TrimSpace(href)
	if u, err := url.Parse(href); err == nil && u.Path != "" {
		return u.Path
	}
	return href
}

func (c *CaldavClient) PutEvent(ctx context.Context, path, uid string, data EventData) (RemoteEvent, error) {
	cal := toICal(uid, data, civildate.Location(), time.Now())
	obj, err := c.cal.PutCalendarObject(ctx, path, cal)
	if err != nil {
		return RemoteEvent{}, fmt.Errorf("icloudcal: putting event at %s%s: %w", c.endpoint, path, err)
	}
	href, etag := obj.Path, obj.ETag
	if href == "" {
		href = path
	}
	// Some servers don't return an ETag on PUT; without one the next pass would think the row is
	// remotely dirty and push again forever. Re-read the object to get it.
	if etag == "" {
		if stored, gerr := c.cal.GetCalendarObject(ctx, href); gerr == nil {
			etag = stored.ETag
		}
	}
	return RemoteEvent{Href: href, UID: uid, ETag: etag, Data: data}, nil
}

func (c *CaldavClient) DeleteEvent(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.endpoint+path, nil)
	if err != nil {
		return fmt.Errorf("icloudcal: building delete request for %s%s: %w", c.endpoint, path, err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("icloudcal: deleting event at %s%s: %w", c.endpoint, path, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("icloudcal: deleting event at %s%s: unexpected status %d", c.endpoint, path, resp.StatusCode)
	}
	return nil
}
