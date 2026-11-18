//go:build integration

package documents_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/documents"
	"github.com/atlas/atlas-go/internal/documents/postgres"
)

// TestRepository_CreateGetUpdateDelete exercises Service -> postgres.Repository against a real
// Postgres — gated behind the "integration" build tag. Run with:
//
//	go test -tags=integration ./internal/documents/...
//
// against TEST_DATABASE_URL (never DATABASE_URL).
func TestRepository_CreateGetUpdateDelete(t *testing.T) {
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
	service := documents.NewService(repo, nil, nil, nil, nil, nil, nil)

	created, err := service.Create(ctx, documents.CreateDocumentRequest{
		OriginalFilename: "integration-test.pdf",
		FileType:         documents.FileTypePDF,
		Origin:           documents.OriginPersonal,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM documents WHERE id = $1`, created.ID) //nolint:errcheck

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.OriginalFilename != "integration-test.pdf" || got.FileType != documents.FileTypePDF {
		t.Fatalf("unexpected document after create: %+v", got)
	}

	tags := "reviewed"
	updated, err := service.Update(ctx, created.ID, documents.UpdateDocumentRequest{Tags: &tags})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Tags == nil || *updated.Tags != "reviewed" {
		t.Fatalf("expected tags to be updated, got %v", updated.Tags)
	}
	if updated.HasFile != created.HasFile {
		t.Fatalf("expected the file association to survive update untouched, got %v", updated.HasFile)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := service.Get(ctx, created.ID); err == nil {
		t.Fatal("expected Get to fail after Delete")
	}
}
