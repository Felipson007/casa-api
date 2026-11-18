// Command drive-migrate moves every locally-stored document of each tenant to that tenant's own
// Google Drive, rewriting FilePath to "googledrive:{id}". It never deletes the local copy, so the
// migration is reversible.
//
// Usage (reads the same env as the API: DATABASE_URL, TENANT_MASTER_KEY, GOOGLE_CLIENT_ID/SECRET,
// APP_UPLOADS_DIR):
//
//	go run ./cmd/drive-migrate                 # dry-run (default): report only
//	go run ./cmd/drive-migrate -dry-run=false  # apply
//	go run ./cmd/drive-migrate -tenant owner   # one tenant only
//
// A tenant whose Drive isn't connected fails its run rather than silently skipping.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/atlas/atlas-go/internal/config"
	"github.com/atlas/atlas-go/internal/documents"
	documentspostgres "github.com/atlas/atlas-go/internal/documents/postgres"
	"github.com/atlas/atlas-go/internal/integrations"
	integrationspostgres "github.com/atlas/atlas-go/internal/integrations/postgres"
	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/platform/keyring"
	"github.com/atlas/atlas-go/internal/tenancy"
)

func main() {
	dryRun := flag.Bool("dry-run", true, "only report what would be migrated; pass -dry-run=false to apply")
	tenantSlug := flag.String("tenant", "", "migrate only this tenant slug (default: every tenant)")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, cfg.DBStatementTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db connect:", err)
		os.Exit(1)
	}
	defer pool.Close()

	registry := tenancy.NewRegistry(pool)
	kr, err := keyring.New(cfg.TenantMasterKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, "keyring:", err)
		os.Exit(1)
	}
	if !kr.Configured() {
		fmt.Fprintln(os.Stderr, "TENANT_MASTER_KEY is required to decrypt local uploads")
		os.Exit(1)
	}
	tenantKeys := tenancy.NewKeyProvider(registry, kr)

	drive := documents.NewGoogleDriveClient(cfg.GoogleClientID, cfg.GoogleClientSecret,
		integrations.NewService(integrationspostgres.NewRepository(pool), nil))
	storage := documents.NewLocalStorage(cfg.UploadsDir, tenantKeys)
	svc := documents.NewService(documentspostgres.NewRepository(pool), storage, drive, nil, nil, nil, nil)

	tenants, err := registry.List(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list tenants:", err)
		os.Exit(1)
	}
	if len(tenants) == 0 {
		fmt.Println("no tenants registered")
		return
	}

	mode := "DRY-RUN"
	if !*dryRun {
		mode = "APPLY"
	}
	fmt.Printf("drive-migrate (%s)\n", mode)

	var migrated, pending, failed int
	for _, t := range tenants {
		if *tenantSlug != "" && t.Slug != *tenantSlug {
			continue
		}
		tctx := tenancy.WithTenantID(db.WithTenantSchema(ctx, t.SchemaName), t.ID)
		res, err := svc.MigrateLocalFilesToDrive(tctx, *dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] FAILED: %v\n", t.Slug, err)
			os.Exit(1)
		}
		fmt.Printf("[%s] schema=%s scanned=%d migrated=%d pending=%d skipped=%d failures=%d\n",
			t.Slug, t.SchemaName, res.Scanned, res.Migrated, res.Pending, res.Skipped, len(res.Failures))
		for _, f := range res.Failures {
			fmt.Printf("    ! %s (%s): %s\n", f.OriginalFilename, f.DocumentID, f.Reason)
		}
		migrated += res.Migrated
		pending += res.Pending
		failed += len(res.Failures)
	}
	fmt.Printf("%s done: migrated=%d pending=%d failures=%d\n", mode, migrated, pending, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
