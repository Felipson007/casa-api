package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	markers map[uuid.UUID]MarkerCatalog
	records map[uuid.UUID]Record
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		markers: make(map[uuid.UUID]MarkerCatalog),
		records: make(map[uuid.UUID]Record),
	}
}

func (f *fakeRepository) ListMarkers(_ context.Context) ([]MarkerCatalog, error) {
	var result []MarkerCatalog
	for _, m := range f.markers {
		result = append(result, m)
	}
	return result, nil
}

func (f *fakeRepository) FindMarkerByID(_ context.Context, id uuid.UUID) (*MarkerCatalog, error) {
	m, ok := f.markers[id]
	if !ok {
		return nil, nil
	}
	return &m, nil
}

func (f *fakeRepository) CreateMarker(_ context.Context, m MarkerCatalog) error {
	f.markers[m.ID] = m
	return nil
}

func (f *fakeRepository) UpdateMarker(_ context.Context, m MarkerCatalog) error {
	if _, ok := f.markers[m.ID]; !ok {
		return errors.New("not found")
	}
	f.markers[m.ID] = m
	return nil
}

func (f *fakeRepository) DeleteMarker(_ context.Context, id uuid.UUID) error {
	delete(f.markers, id)
	return nil
}

func (f *fakeRepository) ListRecords(_ context.Context, personID, markerCatalogID *uuid.UUID) ([]Record, error) {
	var result []Record
	for _, r := range f.records {
		if personID != nil && r.PersonID != *personID {
			continue
		}
		if markerCatalogID != nil && (r.MarkerCatalogID == nil || *r.MarkerCatalogID != *markerCatalogID) {
			continue
		}
		result = append(result, r)
	}
	return result, nil
}

func (f *fakeRepository) FindRecordByID(_ context.Context, id uuid.UUID) (*Record, error) {
	r, ok := f.records[id]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

func (f *fakeRepository) CreateRecord(_ context.Context, r Record) error {
	f.records[r.ID] = r
	return nil
}

func (f *fakeRepository) UpdateRecord(_ context.Context, r Record) error {
	if _, ok := f.records[r.ID]; !ok {
		return errors.New("not found")
	}
	f.records[r.ID] = r
	return nil
}

func (f *fakeRepository) DeleteRecord(_ context.Context, id uuid.UUID) error {
	delete(f.records, id)
	return nil
}

func TestService_Marker_CreateGetListUpdateDelete(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	created, err := service.CreateMarker(ctx, CreateMarkerCatalogRequest{Name: "Glucose"})
	if err != nil {
		t.Fatalf("CreateMarker returned error: %v", err)
	}

	got, err := service.GetMarker(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMarker returned error: %v", err)
	}
	if got.Name != "Glucose" {
		t.Fatalf("expected name Glucose, got %q", got.Name)
	}

	list, err := service.ListMarkers(ctx)
	if err != nil {
		t.Fatalf("ListMarkers returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 marker, got %d", len(list))
	}

	updated, err := service.UpdateMarker(ctx, created.ID, UpdateMarkerCatalogRequest{Name: "Fasting Glucose"})
	if err != nil {
		t.Fatalf("UpdateMarker returned error: %v", err)
	}
	if updated.Name != "Fasting Glucose" {
		t.Fatalf("expected updated name, got %q", updated.Name)
	}

	if err := service.DeleteMarker(ctx, created.ID); err != nil {
		t.Fatalf("DeleteMarker returned error: %v", err)
	}
	if _, err := service.GetMarker(ctx, created.ID); err == nil {
		t.Fatal("expected GetMarker to fail after delete")
	}
}

func TestService_Record_CreateGetListUpdateDelete(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()
	personID := uuid.New()

	created, err := service.CreateRecord(ctx, CreateRecordRequest{
		PersonID:   personID,
		Type:       RecordTypeLabResult,
		RecordDate: civildate.Of(time.Now()),
	})
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}
	if created.Status != StatusDone {
		t.Fatalf("expected a newly created record to be DONE, got %v", created.Status)
	}

	got, err := service.GetRecord(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}
	if got.PersonID != personID {
		t.Fatalf("expected person ID %v, got %v", personID, got.PersonID)
	}

	list, err := service.ListRecords(ctx, &personID, nil)
	if err != nil {
		t.Fatalf("ListRecords returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 record, got %d", len(list))
	}

	updated, err := service.UpdateRecord(ctx, created.ID, UpdateRecordRequest{
		Type:       RecordTypeLabResult,
		RecordDate: created.RecordDate,
		Status:     StatusCancelled,
	})
	if err != nil {
		t.Fatalf("UpdateRecord returned error: %v", err)
	}
	if updated.Status != StatusCancelled {
		t.Fatalf("expected CANCELLED status after update, got %v", updated.Status)
	}
	if updated.PersonID != personID {
		t.Fatalf("expected personID to survive update unchanged, got %v", updated.PersonID)
	}

	if err := service.DeleteRecord(ctx, created.ID); err != nil {
		t.Fatalf("DeleteRecord returned error: %v", err)
	}
	if _, err := service.GetRecord(ctx, created.ID); err == nil {
		t.Fatal("expected GetRecord to fail after delete")
	}
}

func TestService_GetMarker_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.GetMarker(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_GetRecord_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo)
	ctx := context.Background()

	_, err := service.GetRecord(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}
