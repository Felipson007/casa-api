package googlecal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/atlas/atlas-go/internal/calendar"
	"github.com/atlas/atlas-go/internal/calendarsync"
	"github.com/atlas/atlas-go/internal/integrations"
	"github.com/atlas/atlas-go/internal/platform/apperr"
)

const (
	DefaultInterval     = 15 * time.Minute
	defaultWindowPast   = 90 * 24 * time.Hour
	defaultWindowFuture = 730 * 24 * time.Hour
	autoRetryGap        = 5 * time.Minute
)

// Deps wires a Service.
type Deps struct {
	Store       Store
	Credentials CredentialSource
	Connector   Connector
	// Materialize, when set, refreshes the Atlas-owned (read-only) events before a sync runs.
	Materialize  func(ctx context.Context) error
	NewClient    NewClientFunc
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
		d.NewClient = func(ctx context.Context, cfg *oauth2.Config, refreshToken, calendarID string) (Client, error) {
			return NewGoogleAPI(ctx, cfg, refreshToken, calendarID)
		}
	}
	return &Service{Deps: d}
}

// CalendarOption is one selectable Google calendar.
type CalendarOption struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// StatusReport is what Configurações → Conexões shows. Never carries the refresh token.
type StatusReport struct {
	Configured   bool            `json:"configured"`
	Account      *string         `json:"account,omitempty"`
	Calendar     *CalendarOption `json:"calendar,omitempty"`
	LastSyncedAt *time.Time      `json:"lastSyncedAt,omitempty"`
	LastError    string          `json:"lastError,omitempty"`
}

func (s *Service) Status(ctx context.Context) (StatusReport, error) {
	creds, ok, err := s.Credentials.GoogleCalendarCredentials(ctx)
	if err != nil {
		return StatusReport{}, err
	}
	if !ok {
		return StatusReport{}, nil
	}
	rep := StatusReport{Configured: true, LastSyncedAt: creds.LastSyncedAt, LastError: creds.LastError}
	if creds.CalendarID != "" {
		rep.Calendar = &CalendarOption{Path: creds.CalendarID, Name: creds.CalendarName}
	}
	return rep, nil
}

// Calendars lists the account's writable calendars using the stored credentials.
func (s *Service) Calendars(ctx context.Context) ([]CalendarOption, error) {
	creds, ok, err := s.Credentials.GoogleCalendarCredentials(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apperr.Validation("connect Google Calendar first")
	}
	client, err := s.newClient(ctx, creds, creds.CalendarID)
	if err != nil {
		return nil, err
	}
	cals, err := client.ListCalendars(ctx)
	if err != nil {
		return nil, apperr.Validation("não foi possível listar os calendários do Google: "+err.Error(), "calendarId")
	}
	return toOptions(cals), nil
}

// BeginConnect returns the Google consent URL (the handler stores the CSRF state in a cookie).
func (s *Service) BeginConnect() (state, authURL string, err error) {
	if s.Connector == nil {
		return "", "", apperr.Internal(errors.New("googlecal: no connector configured"))
	}
	return s.Connector.BeginGoogleCalendarConnect()
}

// CompleteConnect finishes the OAuth callback.
func (s *Service) CompleteConnect(ctx context.Context, code string) (integrations.ExternalIntegrationDTO, error) {
	if s.Connector == nil {
		return integrations.ExternalIntegrationDTO{}, apperr.Internal(errors.New("googlecal: no connector configured"))
	}
	return s.Connector.CompleteGoogleCalendarConnect(ctx, code)
}

// Connect selects (or changes) the calendar to sync.
func (s *Service) Connect(ctx context.Context, req integrations.ConnectGoogleCalendarRequest) (integrations.ExternalIntegrationDTO, error) {
	if s.Connector == nil {
		return integrations.ExternalIntegrationDTO{}, apperr.Internal(errors.New("googlecal: no connector configured"))
	}
	return s.Connector.ConnectGoogleCalendar(ctx, req)
}

// SyncNow runs a full reconciliation immediately (the "Sincronizar agora" button).
func (s *Service) SyncNow(ctx context.Context) (Report, error) {
	s.materialize(ctx)
	creds, ok, err := s.Credentials.GoogleCalendarCredentials(ctx)
	if err != nil {
		return Report{}, err
	}
	if !ok {
		return Report{}, apperr.Validation("connect Google Calendar first")
	}
	if creds.CalendarID == "" {
		return Report{}, apperr.Validation("choose a Google calendar first")
	}
	client, err := s.newClient(ctx, creds, creds.CalendarID)
	if err != nil {
		return Report{}, err
	}
	rep, err := s.syncAll(ctx, client, creds)
	s.record(ctx, err, rep)
	return rep, err
}

// Tick is the worker entry point (see icloudcal for the same contract).
func (s *Service) Tick(ctx context.Context) {
	s.materialize(ctx)
	creds, ok, err := s.Credentials.GoogleCalendarCredentials(ctx)
	if err != nil || !ok || creds.CalendarID == "" {
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

	client, err := s.newClient(ctx, creds, creds.CalendarID)
	if err != nil {
		s.record(ctx, err, Report{})
		return
	}
	rep, err := s.syncAll(ctx, client, creds)
	s.record(ctx, err, rep)
	if err != nil {
		s.Logger.Error("googlecal: sync failed", "cause", err.Error())
		return
	}
	s.Logger.Info("googlecal: synced", "created", rep.Created, "pushed", rep.Pushed, "pulled", rep.Pulled, "deleted", rep.Deleted, "errors", rep.Errors)
}

func (s *Service) materialize(ctx context.Context) {
	if s.Materialize == nil {
		return
	}
	if err := s.Materialize(ctx); err != nil {
		s.Logger.Warn("googlecal: materializing derived events failed", "cause", err.Error())
	}
}

func (s *Service) newClient(ctx context.Context, creds integrations.GoogleCalendarCredentials, calendarID string) (Client, error) {
	cfg := s.Credentials.GoogleCalendarOAuth()
	if cfg == nil {
		return nil, apperr.Internal(errors.New("googlecal: google oauth is not configured"))
	}
	return s.NewClient(ctx, cfg.Config(), creds.RefreshToken, calendarID)
}

func (s *Service) syncAll(ctx context.Context, client Client, creds integrations.GoogleCalendarCredentials) (Report, error) {
	return calendarsync.Sync(ctx, calendarsync.Params{
		Client:       client,
		Store:        s.Store,
		Provider:     provider,
		Source:       calendar.SourceGoogle,
		CalendarID:   creds.CalendarID,
		WindowPast:   s.WindowPast,
		WindowFuture: s.WindowFuture,
		Now:          s.Now(),
		Logf: func(msg string, err error) {
			s.Logger.Warn("googlecal: "+msg, "cause", err.Error())
		},
	})
}

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
	if err := s.Connector.RecordGoogleCalendarSync(ctx, s.Now(), msg); err != nil {
		s.Logger.Warn("googlecal: recording sync state failed", "cause", err.Error())
	}
}

func toOptions(cals []RemoteCalendar) []CalendarOption {
	out := make([]CalendarOption, 0, len(cals))
	for _, c := range cals {
		out = append(out, CalendarOption{Path: c.Path, Name: c.Name})
	}
	return out
}
