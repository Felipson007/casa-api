package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// ICloudCredentials is everything Atlas needs to talk to the household's iCloud Calendar over
// CalDAV. Stored as ONE AES-GCM-encrypted JSON blob in external_integrations (type
// ICLOUD_CALENDAR), the same path Telegram/Pluggy use — never returned by the API or
// logged. CalendarPath/CalendarName and the sync bookkeeping live here too so there is no extra
// table for a single connection.
type ICloudCredentials struct {
	AppleID     string `json:"appleId"`
	AppPassword string `json:"appPassword"`
	// CalendarPath is the selected CalDAV collection path (e.g. "/12345678/calendars/home/").
	CalendarPath string `json:"calendarPath,omitempty"`
	CalendarName string `json:"calendarName,omitempty"`
	// LastSyncedAt is the last successful reconciliation, used to rate-limit the worker tick.
	LastSyncedAt *time.Time `json:"lastSyncedAt,omitempty"`
	// LastError is the most recent sync failure, surfaced in Configurações → Conexões.
	LastError string `json:"lastError,omitempty"`
}

// ConnectICloudRequest is the body of PUT /api/integrations/icloud-calendar/connection.
// AppleID/AppPassword may be blank on a reconnect to keep the stored values. CalendarPath selects
// which iCloud calendar to sync (blank keeps the current selection).
type ConnectICloudRequest struct {
	AppleID      string `json:"appleId"`
	AppPassword  string `json:"appPassword"`
	CalendarPath string `json:"calendarPath"`
	CalendarName string `json:"calendarName"`
}

// maskAppleID shows just enough for the UI to say "connected as gus…@icloud.com" without exposing
// the whole address.
func maskAppleID(id string) string {
	id = strings.TrimSpace(id)
	at := strings.LastIndex(id, "@")
	if at <= 0 {
		if len(id) <= 4 {
			return id
		}
		return id[:4] + "…"
	}
	local, domain := id[:at], id[at:]
	if len(local) <= 3 {
		return local + "…" + domain
	}
	return local[:3] + "…" + domain
}

// ConnectICloud stores (or updates) the iCloud Calendar connection. It does NOT validate the
// credentials — the caller (internal/icloudcal) probes CalDAV first, so a bad app password never
// gets persisted.
func (s *Service) ConnectICloud(ctx context.Context, req ConnectICloudRequest) (ExternalIntegrationDTO, error) {
	existing, err := s.repo.FindExternalByType(ctx, TypeICloudCalendar)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: looking up icloud connection: %w", err)
	}

	creds := ICloudCredentials{
		AppleID:      strings.TrimSpace(req.AppleID),
		AppPassword:  strings.TrimSpace(req.AppPassword),
		CalendarPath: strings.TrimSpace(req.CalendarPath),
		CalendarName: strings.TrimSpace(req.CalendarName),
	}
	id := uuid.New()
	if existing != nil {
		id = existing.ID
		prev, derr := decodeICloud(existing)
		if derr != nil {
			return ExternalIntegrationDTO{}, derr
		}
		if creds.AppleID == "" {
			creds.AppleID = prev.AppleID
		}
		if creds.AppPassword == "" {
			creds.AppPassword = prev.AppPassword
		}
		if creds.CalendarPath == "" {
			creds.CalendarPath, creds.CalendarName = prev.CalendarPath, prev.CalendarName
		}
		creds.LastSyncedAt, creds.LastError = prev.LastSyncedAt, prev.LastError
	}
	if creds.AppleID == "" || creds.AppPassword == "" {
		return ExternalIntegrationDTO{}, apperr.Validation("appleId and appPassword are required to connect for the first time", "appPassword")
	}

	if err := s.storeICloud(ctx, id, creds); err != nil {
		return ExternalIntegrationDTO{}, err
	}
	persisted, err := s.repo.FindExternalByType(ctx, TypeICloudCalendar)
	if err != nil {
		return ExternalIntegrationDTO{}, fmt.Errorf("integrations: re-reading after icloud connect: %w", err)
	}
	return toExternalDTO(*persisted), nil
}

// RecordICloudSync updates the sync bookkeeping after a run (success or failure). Best-effort: a
// missing connection is a no-op, since the caller already holds valid credentials.
func (s *Service) RecordICloudSync(ctx context.Context, lastSyncedAt time.Time, syncErr string) error {
	existing, err := s.repo.FindExternalByType(ctx, TypeICloudCalendar)
	if err != nil {
		return fmt.Errorf("integrations: looking up icloud connection: %w", err)
	}
	if existing == nil {
		return nil
	}
	creds, err := decodeICloud(existing)
	if err != nil {
		return err
	}
	if syncErr == "" {
		creds.LastSyncedAt = &lastSyncedAt
		creds.LastError = ""
	} else {
		creds.LastError = syncErr
	}
	return s.storeICloud(ctx, existing.ID, creds)
}

// ICloudCredentials returns the decrypted credentials, or ok=false when iCloud isn't connected.
// Used by the background sync only; never exposed over HTTP.
func (s *Service) ICloudCredentials(ctx context.Context) (creds ICloudCredentials, ok bool, err error) {
	existing, err := s.repo.FindExternalByType(ctx, TypeICloudCalendar)
	if err != nil {
		return ICloudCredentials{}, false, fmt.Errorf("integrations: looking up icloud connection: %w", err)
	}
	if existing == nil {
		return ICloudCredentials{}, false, nil
	}
	creds, err = decodeICloud(existing)
	if err != nil {
		return ICloudCredentials{}, false, err
	}
	return creds, true, nil
}

// storeICloud encrypts creds into a fresh ICLOUD_CALENDAR row and upserts it.
func (s *Service) storeICloud(ctx context.Context, id uuid.UUID, creds ICloudCredentials) error {
	payload, err := json.Marshal(creds)
	if err != nil {
		return apperr.Internal(err)
	}
	encrypted, err := Encrypt(string(payload))
	if err != nil {
		return apperr.Internal(err)
	}
	account := maskAppleID(creds.AppleID)
	e := ExternalIntegration{
		ID:                    id,
		Type:                  TypeICloudCalendar,
		ConnectedAccount:      &account,
		RefreshTokenEncrypted: encrypted.Ciphertext,
		RefreshTokenIV:        encrypted.IV,
	}
	return s.repo.UpsertExternal(ctx, e)
}

func decodeICloud(e *ExternalIntegration) (ICloudCredentials, error) {
	plain, err := Decrypt(e.RefreshTokenEncrypted, e.RefreshTokenIV)
	if err != nil {
		return ICloudCredentials{}, fmt.Errorf("integrations: decrypting icloud credentials: %w", err)
	}
	var creds ICloudCredentials
	if err := json.Unmarshal([]byte(plain), &creds); err != nil {
		return ICloudCredentials{}, fmt.Errorf("integrations: stored icloud credentials are malformed: %w", err)
	}
	return creds, nil
}
