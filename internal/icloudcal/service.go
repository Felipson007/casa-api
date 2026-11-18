package icloudcal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/atlas/atlas-go/internal/calendar"
	"github.com/atlas/atlas-go/internal/calendarsync"
	"github.com/atlas/atlas-go/internal/integrations"
	"github.com/atlas/atlas-go/internal/platform/apperr"
)

const (
	// DefaultInterval is how often the worker re-syncs on its own.
	DefaultInterval = 15 * time.Minute
	// defaultWindowPast/defaultWindowFuture bound which events participate in a sync: older or
	// far-future events are left untouched, which keeps the pull manageable for a personal
	// calendar. Events outside the window are never deleted by the sync.
	defaultWindowPast   = 90 * 24 * time.Hour
	defaultWindowFuture = 730 * 24 * time.Hour
	// autoRetryGap stops a failing connection from being hammered every worker tick.
	autoRetryGap = 5 * time.Minute
)

// Deps wires a Service. Store, Credentials and Connector are mandatory; NewClient defaults to the
// real CalDAV adapter.
type Deps struct {
	Store       Store
	Credentials CredentialSource
	Connector   Connector
	// Materialize, when set, refreshes the Atlas-owned (read-only) events before a sync runs.
	Materialize  func(ctx context.Context) error
	NewClient    func(ctx context.Context, appleID, appPassword string) (Client, error)
	Now          func() time.Time
	Logger       *slog.Logger
	Interval     time.Duration
	WindowPast   time.Duration
	WindowFuture time.Duration
}

type Service struct {
	Deps
	mu          sync.Mutex
	lastAttempt time.Time
}

func NewService(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Interval <= 0 {
		d.Interval = DefaultInterval
	}
	if d.WindowPast <= 0 {
		d.WindowPast = defaultWindowPast
	}
	if d.WindowFuture <= 0 {
		d.WindowFuture = defaultWindowFuture
	}
	if d.NewClient == nil {
		d.NewClient = func(ctx context.Context, appleID, appPassword string) (Client, error) {
			return NewCaldavClient(ctx, appleID, appPassword)
		}
	}
	return &Service{Deps: d}
}

// CalendarOption is one selectable iCloud calendar.
type CalendarOption struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// StatusReport is what Configurações → Conexões shows. Never carries the app password.
type StatusReport struct {
	Configured   bool            `json:"configured"`
	Account      *string         `json:"account,omitempty"`
	Calendar     *CalendarOption `json:"calendar,omitempty"`
	LastSyncedAt *time.Time      `json:"lastSyncedAt,omitempty"`
	LastError    string          `json:"lastError,omitempty"`
}

// Status reports whether iCloud is connected and how the last sync went.
func (s *Service) Status(ctx context.Context) (StatusReport, error) {
	creds, ok, err := s.Credentials.ICloudCredentials(ctx)
	if err != nil {
		return StatusReport{}, err
	}
	if !ok {
		return StatusReport{}, nil
	}
	rep := StatusReport{
		Configured:   true,
		Account:      &creds.AppleID,
		LastSyncedAt: creds.LastSyncedAt,
		LastError:    creds.LastError,
	}
	if creds.CalendarPath != "" {
		rep.Calendar = &CalendarOption{Path: creds.CalendarPath, Name: creds.CalendarName}
	}
	return rep, nil
}

// Calendars lists the account's calendars using the stored credentials (used to populate the
// picker in the UI).
func (s *Service) Calendars(ctx context.Context) ([]CalendarOption, error) {
	creds, ok, err := s.Credentials.ICloudCredentials(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apperr.Validation("connect iCloud first")
	}
	client, err := s.NewClient(ctx, creds.AppleID, creds.AppPassword)
	if err != nil {
		return nil, err
	}
	cals, err := client.ListCalendars(ctx)
	if err != nil {
		return nil, apperr.Validation("não foi possível listar os calendários do iCloud", "appleId")
	}
	return toOptions(cals), nil
}

// Connect validates the credentials against CalDAV, stores them (encrypted) and returns the
// account's calendars so the UI can offer a picker. A blank appleId/appPassword keeps the stored
// values; a blank calendarPath keeps the current selection.
func (s *Service) Connect(ctx context.Context, req integrations.ConnectICloudRequest) (integrations.ExternalIntegrationDTO, []CalendarOption, error) {
	if s.Connector == nil {
		return integrations.ExternalIntegrationDTO{}, nil, apperr.Internal(errors.New("icloudcal: no connector configured"))
	}

	appleID := strings.TrimSpace(req.AppleID)
	appPassword := strings.TrimSpace(req.AppPassword)
	if appleID == "" || appPassword == "" {
		creds, ok, err := s.Credentials.ICloudCredentials(ctx)
		if err != nil {
			return integrations.ExternalIntegrationDTO{}, nil, err
		}
		if !ok {
			return integrations.ExternalIntegrationDTO{}, nil, apperr.Validation("appleId and appPassword are required to connect for the first time", "appPassword")
		}
		if appleID == "" {
			appleID = creds.AppleID
		}
		if appPassword == "" {
			appPassword = creds.AppPassword
		}
	}

	client, err := s.NewClient(ctx, appleID, appPassword)
	if err != nil {
		return integrations.ExternalIntegrationDTO{}, nil, err
	}
	cals, err := client.ListCalendars(ctx)
	if err != nil {
		return integrations.ExternalIntegrationDTO{}, nil, apperr.Validation("não foi possível autenticar no iCloud — confira o Apple ID e a senha de app", "appPassword")
	}
	options := toOptions(cals)
	if req.CalendarPath != "" && !containsPath(options, req.CalendarPath) {
		return integrations.ExternalIntegrationDTO{}, nil, apperr.Validation("calendarPath does not belong to this account", "calendarPath")
	}

	dto, err := s.Connector.ConnectICloud(ctx, req)
	if err != nil {
		return integrations.ExternalIntegrationDTO{}, nil, err
	}
	return dto, options, nil
}

// SyncNow runs a full reconciliation immediately (the "Sincronizar agora" button).
func (s *Service) SyncNow(ctx context.Context) (Report, error) {
	s.materialize(ctx)
	creds, ok, err := s.Credentials.ICloudCredentials(ctx)
	if err != nil {
		return Report{}, err
	}
	if !ok {
		return Report{}, apperr.Validation("connect iCloud Calendar first")
	}
	if creds.CalendarPath == "" {
		return Report{}, apperr.Validation("choose an iCloud calendar first")
	}
	client, err := s.NewClient(ctx, creds.AppleID, creds.AppPassword)
	if err != nil {
		return Report{}, err
	}
	rep, err := s.syncAll(ctx, client, creds)
	s.record(ctx, err, rep)
	return rep, err
}

// materialize refreshes the derived (read-only) events before a sync; a failure is logged and the
// sync proceeds with whatever rows already exist.
func (s *Service) materialize(ctx context.Context) {
	if s.Materialize == nil {
		return
	}
	if err := s.Materialize(ctx); err != nil {
		s.Logger.Warn("icloudcal: materializing derived events failed", "cause", err.Error())
	}
}

// syncAll reconciles with the selected calendar through the shared reconciliation core.
func (s *Service) syncAll(ctx context.Context, client Client, creds integrations.ICloudCredentials) (Report, error) {
	return calendarsync.Sync(ctx, calendarsync.Params{
		Client:       client,
		Store:        s.Store,
		Provider:     provider,
		Source:       calendar.SourceICloud,
		CalendarID:   creds.CalendarPath,
		HrefFor:      func(uid string) string { return joinPath(creds.CalendarPath, uid+".ics") },
		WindowPast:   s.WindowPast,
		WindowFuture: s.WindowFuture,
		Now:          s.Now(),
		Logf: func(msg string, err error) {
			s.Logger.Warn("icloudcal: "+msg, "cause", err.Error())
		},
	})
}

// ProbeReport is a structural, non-sensitive diagnostic of the iCloud connection: no calendar
// name, event title or id ever appears here.
type ProbeReport struct {
	Endpoint     string `json:"endpoint"`
	Calendars    int    `json:"calendars"`
	EventsInWin  int    `json:"eventsInWindow"`
	CreateOK     bool   `json:"createOk"`
	CreateErr    string `json:"createError,omitempty"`
	DeleteOK     bool   `json:"deleteOk"`
	DeleteErr    string `json:"deleteError,omitempty"`
	UsedCalendar bool   `json:"usedStoredCalendar"`
}

// Probe validates the whole write path against the real CalDAV server using the stored
// credentials: discovery endpoint, calendar listing, a window query, then a create/delete
// round-trip. It exists so the homelab can answer "why did the push fail?" — the same job the
// pluggy-probe does for Open Finance.
func (s *Service) Probe(ctx context.Context) (ProbeReport, error) {
	creds, ok, err := s.Credentials.ICloudCredentials(ctx)
	if err != nil {
		return ProbeReport{}, err
	}
	if !ok {
		return ProbeReport{}, apperr.Validation("connect iCloud Calendar first")
	}
	client, err := s.NewClient(ctx, creds.AppleID, creds.AppPassword)
	if err != nil {
		return ProbeReport{}, err
	}

	rep := ProbeReport{UsedCalendar: creds.CalendarPath != ""}
	if e, ok := client.(interface{ Endpoint() string }); ok {
		rep.Endpoint = e.Endpoint()
	}

	cals, err := client.ListCalendars(ctx)
	if err != nil {
		return rep, apperr.Validation("list calendars failed: "+err.Error(), "appleId")
	}
	rep.Calendars = len(cals)

	path := creds.CalendarPath
	if path == "" && len(cals) > 0 {
		path = cals[0].Path
	}
	if path == "" {
		return rep, apperr.Validation("no calendar available", "calendarPath")
	}

	now := s.Now()
	events, err := client.ListEvents(ctx, path, now.AddDate(0, 0, -30), now.AddDate(0, 0, 30))
	if err != nil {
		return rep, apperr.Validation("list events failed: "+err.Error(), "appleId")
	}
	rep.EventsInWin = len(events)

	uid := fmt.Sprintf("atlas-probe-%d", now.UnixNano())
	stored, err := client.PutEvent(ctx, joinPath(path, uid+".ics"), uid, EventData{
		Title:     "Atlas probe",
		AllDay:    true,
		StartDate: now.AddDate(0, 0, 1),
	})
	if err != nil {
		rep.CreateErr = err.Error()
		return rep, nil
	}
	rep.CreateOK = true
	if err := client.DeleteEvent(ctx, stored.Href); err != nil {
		rep.DeleteErr = err.Error()
		return rep, nil
	}
	rep.DeleteOK = true
	return rep, nil
}

// Tick is what the worker calls every minute: it syncs when the connection has never synced or is
// older than the interval, never more often than autoRetryGap for automatic runs. A no-op when
// iCloud isn't connected or no calendar was chosen yet.
func (s *Service) Tick(ctx context.Context) {
	s.materialize(ctx)
	creds, ok, err := s.Credentials.ICloudCredentials(ctx)
	if err != nil || !ok || creds.CalendarPath == "" {
		return
	}
	now := s.Now()
	if creds.LastSyncedAt != nil && now.Sub(*creds.LastSyncedAt) < s.Interval {
		return
	}
	s.mu.Lock()
	tooSoon := !s.lastAttempt.IsZero() && now.Sub(s.lastAttempt) < autoRetryGap
	if !tooSoon {
		s.lastAttempt = now
	}
	s.mu.Unlock()
	if tooSoon {
		return
	}

	client, err := s.NewClient(ctx, creds.AppleID, creds.AppPassword)
	if err != nil {
		s.record(ctx, err, Report{})
		return
	}
	rep, err := s.syncAll(ctx, client, creds)
	s.record(ctx, err, rep)
	if err != nil {
		s.Logger.Error("icloudcal: sync failed", "cause", err.Error())
		return
	}
	s.Logger.Info("icloudcal: synced", "created", rep.Created, "pushed", rep.Pushed, "pulled", rep.Pulled, "deleted", rep.Deleted, "errors", rep.Errors)
}

// record persists the run outcome so Configurações → Conexões shows the last sync time and any
// failure. A run that completed but had per-event failures is recorded as an error too, with the
// first message — otherwise the UI would claim "tudo em dia" while nothing was actually pushed.
func (s *Service) record(ctx context.Context, syncErr error, rep Report) {
	if s.Connector == nil {
		return
	}
	msg := ""
	switch {
	case syncErr != nil:
		msg = syncErr.Error()
	case rep.Errors > 0:
		msg = fmt.Sprintf("%d evento(s) falharam ao sincronizar: %s", rep.Errors, rep.Error)
	}
	if err := s.Connector.RecordICloudSync(ctx, s.Now(), msg); err != nil {
		s.Logger.Warn("icloudcal: recording sync state failed", "cause", err.Error())
	}
}

func toOptions(cals []RemoteCalendar) []CalendarOption {
	out := make([]CalendarOption, 0, len(cals))
	for _, c := range cals {
		out = append(out, CalendarOption{Path: c.Path, Name: c.Name})
	}
	return out
}

func containsPath(options []CalendarOption, path string) bool {
	for _, o := range options {
		if o.Path == path {
			return true
		}
	}
	return false
}
