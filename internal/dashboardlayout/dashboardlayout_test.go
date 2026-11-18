package dashboardlayout

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved. Mirrors
// what a real ON CONFLICT (app_user_id) DO UPDATE upsert does: last Set wins per user.
type fakeRepository struct {
	byUser map[uuid.UUID]Layout
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byUser: make(map[uuid.UUID]Layout)}
}

func (f *fakeRepository) Get(_ context.Context, appUserID uuid.UUID) (Layout, error) {
	l, ok := f.byUser[appUserID]
	if !ok {
		// Never saved: zero-value layout, not an error — mirrors postgres.Repository.Get.
		return Layout{AppUserID: appUserID}, nil
	}
	return l, nil
}

func (f *fakeRepository) Set(_ context.Context, appUserID uuid.UUID, items []byte) (Layout, error) {
	l := Layout{AppUserID: appUserID, Items: items}
	f.byUser[appUserID] = l
	return l, nil
}

func TestService_Get_EmptyWhenNeverSaved(t *testing.T) {
	service := NewService(newFakeRepository())

	got, err := service.Get(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("expected no error for a user who never saved a layout, got %v", err)
	}
	if got.Layout != nil {
		t.Fatalf("expected a nil (null) layout, got %s", got.Layout)
	}
	if !got.UpdatedAt.IsZero() {
		t.Fatalf("expected zero UpdatedAt for a user who never saved, got %v", got.UpdatedAt)
	}
}

func TestService_SetThenGet_RoundTrips(t *testing.T) {
	service := NewService(newFakeRepository())
	ctx := context.Background()
	appUserID := uuid.New()

	payload := []byte(`[{"i":"life-map","x":0,"y":0,"w":8,"h":4}]`)
	saved, err := service.Set(ctx, appUserID, SetLayoutRequest{Layout: payload})
	if err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if string(saved.Layout) != string(payload) {
		t.Fatalf("expected Set to echo the payload back, got %s", saved.Layout)
	}

	got, err := service.Get(ctx, appUserID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if string(got.Layout) != string(payload) {
		t.Fatalf("expected Get to return the saved payload, got %s", got.Layout)
	}
}

func TestService_Set_RejectsNonArrayJSON(t *testing.T) {
	service := NewService(newFakeRepository())
	ctx := context.Background()
	appUserID := uuid.New()

	cases := []struct {
		name    string
		payload string
	}{
		{"object instead of array", `{"i":"life-map","x":0,"y":0,"w":8,"h":4}`},
		{"malformed JSON", `[{"i":"life-map",`},
		{"empty body", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Set(ctx, appUserID, SetLayoutRequest{Layout: []byte(tc.payload)})
			if err == nil {
				t.Fatal("expected a validation error")
			}
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
				t.Fatalf("expected apperr.KindValidation, got %v", err)
			}
		})
	}
}

func TestService_Set_ScopedPerUser(t *testing.T) {
	service := NewService(newFakeRepository())
	ctx := context.Background()
	userA := uuid.New()
	userB := uuid.New()

	if _, err := service.Set(ctx, userA, SetLayoutRequest{Layout: []byte(`[{"i":"a"}]`)}); err != nil {
		t.Fatalf("Set (userA) returned error: %v", err)
	}

	// userB never saved a layout — must get an empty layout back, not userA's.
	got, err := service.Get(ctx, userB)
	if err != nil {
		t.Fatalf("Get (userB) returned error: %v", err)
	}
	if got.Layout != nil {
		t.Fatalf("expected userB to have no saved layout, got %s", got.Layout)
	}
}

// Guards the wire contract: a never-saved user's DTO must serialize `layout` as `null` (not `[]`),
// which is exactly the sentinel the frontend's useDashboardLayout treats as "never customized" and
// maps to its built-in default layout (see internal/dashboardlayout/domain.go).
func TestLayoutDTO_EmptySerializesAsNull(t *testing.T) {
	raw, err := json.Marshal(toDTO(Layout{AppUserID: uuid.New()}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"layout":null`) {
		t.Fatalf("expected layout to serialize as null, got %s", raw)
	}
}
