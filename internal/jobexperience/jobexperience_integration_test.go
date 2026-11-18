//go:build integration

package jobexperience_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/documents"
	documentspostgres "github.com/atlas/atlas-go/internal/documents/postgres"
	"github.com/atlas/atlas-go/internal/jobexperience"
	"github.com/atlas/atlas-go/internal/jobexperience/postgres"
	"github.com/atlas/atlas-go/internal/people"
	peoplepostgres "github.com/atlas/atlas-go/internal/people/postgres"
	"github.com/atlas/atlas-go/internal/personrelations"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// TestService_CreateUpdateDelete exercises Service -> postgres.Repository against a real Postgres
// — gated behind the "integration" build tag, exactly like documents' and people's own integration
// tests. Confirms the job_experiences schema (FKs to people/documents, the end_date >= start_date
// CHECK constraint) actually works, not just the in-memory fake used by the unit tests. Run with:
//
//	go test -tags=integration ./internal/jobexperience/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestService_CreateUpdateDelete(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	defer pool.Close()

	peopleSvc := people.NewService(peoplepostgres.NewRepository(pool), personrelations.DeriveFromOrigin)
	documentsSvc := documents.NewService(documentspostgres.NewRepository(pool), nil, nil, nil, nil, nil, nil)
	service := jobexperience.NewService(postgres.NewRepository(pool), peopleSvc, documentsSvc)

	person, err := peopleSvc.CreatePerson(ctx, people.CreatePersonRequest{Name: "Integration Test Person"})
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM people WHERE id = $1`, person.ID) //nolint:errcheck

	created, err := service.Create(ctx, jobexperience.CreateExperienceRequest{
		PersonID: person.ID, Company: "Acme Corp", Title: "Engenheiro de Software",
		StartDate: civildate.Of(mustParseDate(t, "2022-03-01")),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM job_experiences WHERE id = $1`, created.ID) //nolint:errcheck

	if !created.IsCurrent || created.Company != "Acme Corp" {
		t.Fatalf("unexpected experience after create: %+v", created)
	}

	end := civildate.Of(mustParseDate(t, "2024-06-30"))
	updated, err := service.Update(ctx, created.ID, jobexperience.UpdateExperienceRequest{
		Company: "Acme Corp", Title: "Engenheiro Sênior", StartDate: created.StartDate, EndDate: &end,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.IsCurrent || updated.Title != "Engenheiro Sênior" {
		t.Fatalf("unexpected experience after update: %+v", updated)
	}

	// The DB's own CHECK constraint (ck_job_experiences_dates) must reject end < start even if
	// somehow reached with the service-layer validation bypassed — belt-and-suspenders.
	badEnd := civildate.Of(mustParseDate(t, "2000-01-01"))
	if _, err := service.Update(ctx, created.ID, jobexperience.UpdateExperienceRequest{
		Company: "Acme Corp", Title: "Engenheiro Sênior", StartDate: created.StartDate, EndDate: &badEnd,
	}); err == nil {
		t.Fatal("expected an error updating with endDate before startDate")
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := service.Get(ctx, created.ID); err == nil {
		t.Fatal("expected an error getting a deleted experience")
	}
}

func mustParseDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing date %q: %v", s, err)
	}
	return d
}
