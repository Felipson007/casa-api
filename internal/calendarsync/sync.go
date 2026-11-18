package calendarsync

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/calendar"
)

// Report is the outcome of one reconciliation. The JSON tags matter: the API returns this struct
// verbatim and the frontend reads the lower-case keys.
type Report struct {
	Created int    `json:"created"`
	Pushed  int    `json:"pushed"`
	Pulled  int    `json:"pulled"`
	Deleted int    `json:"deleted"`
	Errors  int    `json:"errors"`
	Error   string `json:"error,omitempty"`
}

// fail records a per-event failure without aborting the run.
func (r *Report) fail(err error) {
	if err == nil {
		return
	}
	r.Errors++
	if r.Error == "" {
		r.Error = err.Error()
	}
}

// Sync reconciles every live event with the selected remote calendar. Deletions propagate both
// ways; conflicts resolve last-write-wins (remote Modified vs local UpdatedAt). A per-event failure
// is logged and skipped, never aborting the whole run.
func Sync(ctx context.Context, p Params) (Report, error) {
	from := p.Now.Add(-p.WindowPast)
	to := p.Now.Add(p.WindowFuture)
	logf := p.Logf
	if logf == nil {
		logf = func(string, error) {}
	}

	remote, err := p.Client.ListEvents(ctx, p.CalendarID, from, to)
	if err != nil {
		return Report{}, err
	}
	live, err := p.Store.ListLive(ctx, p.Provider, from, to)
	if err != nil {
		return Report{}, fmt.Errorf("calendarsync: listing local events: %w", err)
	}
	deleted, err := p.Store.ListDeletedExternal(ctx, p.Provider)
	if err != nil {
		return Report{}, fmt.Errorf("calendarsync: listing deleted events: %w", err)
	}

	remoteByUID := make(map[string]RemoteEvent, len(remote))
	for _, r := range remote {
		remoteByUID[r.UID] = r
	}

	var rep Report
	matched := make(map[string]bool, len(remote))

	for i := range live {
		e := live[i]
		if e.ExternalID == nil {
			if recentlyFailed(e, p.Now) {
				continue
			}
			if err := syncPush(ctx, p, &e, true, &rep); err != nil {
				logf("push (create) failed", err)
				rep.fail(err)
				markPushFailed(ctx, p, e, err, logf)
			}
			continue
		}

		r, ok := remoteByUID[*e.ExternalID]
		if e.ReadOnly {
			// Atlas-owned (derived from another module): push-only. Never pull an external edit,
			// never delete locally; if it vanished remotely, recreate it.
			if ok {
				matched[r.UID] = true
			}
			localDirty := e.ExternalSyncedAt == nil || e.UpdatedAt.After(*e.ExternalSyncedAt)
			remoteDirty := !ok || e.ETag == nil || *e.ETag != r.ETag
			if !ok {
				if recentlyFailed(e, p.Now) {
					continue
				}
				if err := syncPush(ctx, p, &e, true, &rep); err != nil {
					logf("push (recreate) failed", err)
					rep.fail(err)
					markPushFailed(ctx, p, e, err, logf)
				}
			} else if localDirty || remoteDirty {
				if recentlyFailed(e, p.Now) {
					continue
				}
				if err := syncPush(ctx, p, &e, false, &rep); err != nil {
					logf("push (read-only) failed", err)
					rep.fail(err)
					markPushFailed(ctx, p, e, err, logf)
				}
			}
			continue
		}
		if !ok {
			if err := p.Store.SoftDelete(ctx, e.ID); err != nil {
				logf("soft-delete failed", err)
				rep.fail(err)
			} else {
				rep.Deleted++
			}
			continue
		}
		matched[r.UID] = true

		localDirty := e.ExternalSyncedAt == nil || e.UpdatedAt.After(*e.ExternalSyncedAt)
		remoteDirty := e.ETag == nil || *e.ETag != r.ETag
		switch {
		case localDirty && remoteDirty:
			if r.Modified.After(e.UpdatedAt) {
				if err := syncPull(ctx, p, &e, r, &rep); err != nil {
					logf("pull (conflict) failed", err)
					rep.fail(err)
				}
			} else if recentlyFailed(e, p.Now) {
				break
			} else if err := syncPush(ctx, p, &e, false, &rep); err != nil {
				logf("push (conflict) failed", err)
				rep.fail(err)
				markPushFailed(ctx, p, e, err, logf)
			}
		case remoteDirty:
			if err := syncPull(ctx, p, &e, r, &rep); err != nil {
				logf("pull failed", err)
				rep.fail(err)
			}
		case localDirty:
			if recentlyFailed(e, p.Now) {
				break
			}
			if err := syncPush(ctx, p, &e, false, &rep); err != nil {
				logf("push failed", err)
				rep.fail(err)
				markPushFailed(ctx, p, e, err, logf)
			}
		}
	}

	for _, r := range remote {
		if matched[r.UID] {
			continue
		}
		if err := syncInsertPulled(ctx, p, r, &rep); err != nil {
			logf("insert pulled failed", err)
			rep.fail(err)
		}
	}

	// Local deletions of synced events → remove remotely, then drop the mapping. The mapping is
	// only cleared when the remote delete actually succeeded, so a failed delete is retried.
	for i := range deleted {
		e := deleted[i]
		if e.ExternalHref != nil {
			if err := p.Client.DeleteEvent(ctx, *e.ExternalHref); err != nil {
				logf("remote delete failed", err)
				rep.fail(err)
				continue
			}
		}
		if err := p.Store.ClearExternal(ctx, e.ID); err != nil {
			logf("clearing external mapping failed", err)
			rep.fail(err)
		} else {
			rep.Deleted++
		}
	}

	return rep, nil
}

// recentlyFailed reports whether e's last push attempt failed less than pushRetryGap ago — Sync
// skips it silently until the gap elapses, instead of repeating the identical failing request
// (typically a permanent error like 403 on a read-only/shared remote calendar) on every run.
func recentlyFailed(e calendar.Event, now time.Time) bool {
	return e.SyncFailedAt != nil && now.Sub(*e.SyncFailedAt) < pushRetryGap
}

// markPushFailed persists the failure so the next Sync run can skip retrying via recentlyFailed.
// Best-effort: a failure to record the failure itself is logged but never escalated, since the
// original push error is already what the caller reports.
func markPushFailed(ctx context.Context, p Params, e calendar.Event, cause error, logf func(string, error)) {
	if err := p.Store.MarkPushFailed(ctx, e.ID, cause.Error(), p.Now); err != nil {
		logf("recording push failure failed", err)
	}
}

func syncPush(ctx context.Context, p Params, e *calendar.Event, create bool, rep *Report) error {
	uid := e.ID.String()
	if e.ExternalID != nil && *e.ExternalID != "" {
		uid = *e.ExternalID
	}
	href := ""
	if create {
		if p.HrefFor != nil {
			href = p.HrefFor(uid)
		}
	} else if e.ExternalHref != nil {
		href = *e.ExternalHref
	}

	res, err := p.Client.PutEvent(ctx, href, uid, eventToData(*e))
	if err != nil {
		return err
	}

	providerCopy := p.Provider
	calID := p.CalendarID
	hrefOut := res.Href
	if hrefOut == "" {
		hrefOut = href
	}
	uidOut := res.UID
	if uidOut == "" {
		uidOut = uid
	}
	e.Source = calendar.SourceManual
	e.ExternalProvider = &providerCopy
	e.ExternalID = &uidOut
	e.ExternalCalendarID = &calID
	e.ExternalHref = &hrefOut
	e.SyncError = nil
	e.SyncFailedAt = nil
	if res.ETag != "" {
		etag := res.ETag
		e.ETag = &etag
	}
	syncedAt := p.Now
	e.ExternalSyncedAt = &syncedAt

	if err := p.Store.UpdateSynced(ctx, *e); err != nil {
		return err
	}
	if create {
		rep.Created++
	} else {
		rep.Pushed++
	}
	return nil
}

func syncPull(ctx context.Context, p Params, e *calendar.Event, r RemoteEvent, rep *Report) error {
	applyData(e, r.Data)
	providerCopy := p.Provider
	calID := p.CalendarID
	href := r.Href
	e.ExternalProvider = &providerCopy
	e.ExternalCalendarID = &calID
	e.ExternalHref = &href
	if r.ETag != "" {
		etag := r.ETag
		e.ETag = &etag
	}
	syncedAt := p.Now
	e.ExternalSyncedAt = &syncedAt

	if err := p.Store.UpdateSynced(ctx, *e); err != nil {
		return err
	}
	rep.Pulled++
	return nil
}

func syncInsertPulled(ctx context.Context, p Params, r RemoteEvent, rep *Report) error {
	providerCopy := p.Provider
	calID := p.CalendarID
	href := r.Href
	uid := r.UID
	syncedAt := p.Now
	e := calendar.Event{
		ID:                 uuid.New(),
		Source:             p.Source,
		Title:              r.Data.Title,
		Description:        r.Data.Description,
		Location:           r.Data.Location,
		AllDay:             r.Data.AllDay,
		StartDate:          r.Data.StartDate,
		EndDate:            r.Data.EndDate,
		StartTime:          r.Data.StartTime,
		EndTime:            r.Data.EndTime,
		ExternalProvider:   &providerCopy,
		ExternalID:         &uid,
		ExternalCalendarID: &calID,
		ExternalHref:       &href,
		ExternalSyncedAt:   &syncedAt,
	}
	if r.ETag != "" {
		etag := r.ETag
		e.ETag = &etag
	}
	if err := p.Store.InsertSynced(ctx, e); err != nil {
		return err
	}
	rep.Pulled++
	return nil
}

func eventToData(e calendar.Event) EventData {
	return EventData{
		Title:       e.Title,
		Description: e.Description,
		Location:    e.Location,
		AllDay:      e.AllDay,
		StartDate:   e.StartDate,
		EndDate:     e.EndDate,
		StartTime:   e.StartTime,
		EndTime:     e.EndTime,
	}
}

func applyData(e *calendar.Event, d EventData) {
	e.Title = d.Title
	e.Description = d.Description
	e.Location = d.Location
	e.AllDay = d.AllDay
	e.StartDate = d.StartDate
	e.EndDate = d.EndDate
	e.StartTime = d.StartTime
	e.EndTime = d.EndTime
}
