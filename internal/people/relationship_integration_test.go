//go:build integration

package people_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/people/postgres"
	"github.com/atlas/atlas-go/internal/personrelations"
)

// TestService_CreateRelationship_PersistsAndRejectsDuplicate is a regression test for a bug where
// findRelationshipSQL appended "WHERE r.id = $1" after listRelationshipsSQL's own "ORDER BY
// r.created_at", producing invalid SQL — so the FindRelationship call CreateRelationship makes
// right after a successful insert (service.go) always failed, turning every committed relationship
// into an HTTP 500 even though the row was already persisted. Run with:
//
//	go test -tags=integration ./internal/people/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestService_CreateRelationship_PersistsAndRejectsDuplicate(t *testing.T) {
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

	repo := postgres.NewRepository(pool)
	service := people.NewService(repo, personrelations.DeriveFromOrigin)

	mother, err := service.CreatePerson(ctx, people.CreatePersonRequest{Name: "Integration Test Mother"})
	if err != nil {
		t.Fatalf("CreatePerson(mother): %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM people WHERE id = $1`, mother.ID) //nolint:errcheck

	child, err := service.CreatePerson(ctx, people.CreatePersonRequest{Name: "Integration Test Child"})
	if err != nil {
		t.Fatalf("CreatePerson(child): %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM people WHERE id = $1`, child.ID) //nolint:errcheck

	req := people.CreateRelationshipRequest{FromPersonID: mother.ID, ToPersonID: child.ID, Type: personrelations.MotherOf}

	rel, err := service.CreateRelationship(ctx, req)
	if err != nil {
		t.Fatalf("CreateRelationship: %v (this is the bug findRelationshipSQL's malformed WHERE/ORDER BY caused: the insert commits but the post-create FindRelationship 500s)", err)
	}
	defer pool.Exec(ctx, `DELETE FROM person_relationships WHERE id = $1`, rel.ID) //nolint:errcheck

	if rel.FromName != "Integration Test Mother" || rel.ToName != "Integration Test Child" {
		t.Fatalf("unexpected relationship after create: %+v", rel)
	}

	if _, err := service.CreateRelationship(ctx, req); err == nil {
		t.Fatalf("expected duplicate relationship to be rejected, got nil error")
	}
}
