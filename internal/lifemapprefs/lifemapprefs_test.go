package lifemapprefs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved. Mirrors
// what a real ON CONFLICT (app_user_id) DO UPDATE upsert does: last Set wins per user.
type fakeRepository struct {
	byUser map[uuid.UUID]Preferences
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byUser: make(map[uuid.UUID]Preferences)}
}

func (f *fakeRepository) Get(_ context.Context, appUserID uuid.UUID) (Preferences, error) {
	p, ok := f.byUser[appUserID]
	if !ok {
		// Never saved: empty set, not an error — mirrors postgres.Repository.Get.
		return Preferences{AppUserID: appUserID, HiddenAreas: []string{}}, nil
	}
	return p, nil
}

func (f *fakeRepository) Set(_ context.Context, appUserID uuid.UUID, hiddenAreas []string) (Preferences, error) {
	p := Preferences{AppUserID: appUserID, HiddenAreas: hiddenAreas}
	f.byUser[appUserID] = p
	return p, nil
}

func TestService_Get_EmptyWhenNeverSaved(t *testing.T) {
	service := NewService(newFakeRepository())

	got, err := service.Get(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("expected no error for a user who never saved preferences, got %v", err)
	}
	if got.HiddenAreas == nil {
		t.Fatal("expected an empty slice, not nil")
	}
	if len(got.HiddenAreas) != 0 {
		t.Fatalf("expected no hidden areas, got %v", got.HiddenAreas)
	}
	if !got.UpdatedAt.IsZero() {
		t.Fatalf("expected zero UpdatedAt for a user who never saved, got %v", got.UpdatedAt)
	}
}

func TestService_SetThenGet_RoundTrips(t *testing.T) {
	service := NewService(newFakeRepository())
	ctx := context.Background()
	appUserID := uuid.New()

	saved, err := service.Set(ctx, appUserID, SetPreferencesRequest{HiddenAreas: []string{"contratos", "assinaturas"}})
	if err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if len(saved.HiddenAreas) != 2 {
		t.Fatalf("expected Set to echo back 2 hidden areas, got %v", saved.HiddenAreas)
	}

	got, err := service.Get(ctx, appUserID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if len(got.HiddenAreas) != 2 || got.HiddenAreas[0] != "contratos" || got.HiddenAreas[1] != "assinaturas" {
		t.Fatalf("expected Get to return the saved hidden areas, got %v", got.HiddenAreas)
	}
}

// Regression: a nil/omitted hiddenAreas must never come back as JSON null — see
// docs/code-quality.md, breaks any .map() on the frontend.
func TestService_Set_NilHiddenAreasBecomesEmptySlice(t *testing.T) {
	service := NewService(newFakeRepository())
	ctx := context.Background()
	appUserID := uuid.New()

	saved, err := service.Set(ctx, appUserID, SetPreferencesRequest{HiddenAreas: nil})
	if err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if saved.HiddenAreas == nil {
		t.Fatal("expected HiddenAreas to be an empty slice, not nil")
	}
	if len(saved.HiddenAreas) != 0 {
		t.Fatalf("expected empty slice, got %v", saved.HiddenAreas)
	}
}

func TestService_Set_ScopedPerUser(t *testing.T) {
	service := NewService(newFakeRepository())
	ctx := context.Background()
	userA := uuid.New()
	userB := uuid.New()

	if _, err := service.Set(ctx, userA, SetPreferencesRequest{HiddenAreas: []string{"fiscal"}}); err != nil {
		t.Fatalf("Set (userA) returned error: %v", err)
	}

	// userB never saved preferences — must get an empty set back, not userA's.
	got, err := service.Get(ctx, userB)
	if err != nil {
		t.Fatalf("Get (userB) returned error: %v", err)
	}
	if len(got.HiddenAreas) != 0 {
		t.Fatalf("expected userB to have no saved preferences, got %v", got.HiddenAreas)
	}
}

func TestToDTO_NeverReturnsNilHiddenAreas(t *testing.T) {
	dto := toDTO(Preferences{AppUserID: uuid.New(), HiddenAreas: nil})
	if dto.HiddenAreas == nil {
		t.Fatal("expected toDTO to normalize a nil slice to empty")
	}
}

// Guards the wire contract: a never-saved user's DTO must serialize `hiddenAreas` as `[]`, never
// `null` — the frontend .map()/includes() on it would break on null (see docs/code-quality.md).
func TestPreferencesDTO_EmptySerializesAsEmptyArray(t *testing.T) {
	raw, err := json.Marshal(toDTO(Preferences{AppUserID: uuid.New()}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"hiddenAreas":[]`) {
		t.Fatalf("expected hiddenAreas to serialize as [], got %s", raw)
	}
}
