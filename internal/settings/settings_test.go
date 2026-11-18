package settings

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	row *Settings
}

func newFakeRepository(seed *Settings) *fakeRepository {
	return &fakeRepository{row: seed}
}

func (f *fakeRepository) Get(_ context.Context) (*Settings, error) {
	if f.row == nil {
		return nil, ErrNotFound
	}
	cp := *f.row
	return &cp, nil
}

func (f *fakeRepository) Update(_ context.Context, s *Settings) error {
	if f.row == nil {
		return ErrNotFound
	}
	s.UpdatedAt = time.Now()
	cp := *s
	f.row = &cp
	return nil
}

func TestService_Get(t *testing.T) {
	repo := newFakeRepository(&Settings{CompanyName: "Atlas", PixKey: "atlas@pix.com", LogoPath: "/logo.png"})
	service := NewService(repo)

	got, err := service.Get(context.Background())
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.CompanyName != "Atlas" {
		t.Fatalf("expected company name Atlas, got %q", got.CompanyName)
	}
}

func TestService_Get_NotFound(t *testing.T) {
	repo := newFakeRepository(nil)
	service := NewService(repo)

	_, err := service.Get(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Update(t *testing.T) {
	repo := newFakeRepository(&Settings{CompanyName: "Old Name", PixKey: "old@pix.com", LogoPath: "/old.png"})
	service := NewService(repo)

	updated, err := service.Update(context.Background(), UpdateSettingsRequest{
		CompanyName: "New Name", PixKey: "new@pix.com", LogoPath: "/new.png",
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.CompanyName != "New Name" || updated.PixKey != "new@pix.com" || updated.LogoPath != "/new.png" {
		t.Fatalf("expected updated fields to persist, got %+v", updated)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	repo := newFakeRepository(nil)
	service := NewService(repo)

	_, err := service.Update(context.Background(), UpdateSettingsRequest{CompanyName: "X"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
