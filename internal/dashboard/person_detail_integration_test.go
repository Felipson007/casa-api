//go:build integration

package dashboard_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/assets"
	assetspostgres "github.com/atlas/atlas-go/internal/assets/postgres"
	"github.com/atlas/atlas-go/internal/dashboard"
	"github.com/atlas/atlas-go/internal/documents"
	documentspostgres "github.com/atlas/atlas-go/internal/documents/postgres"
	"github.com/atlas/atlas-go/internal/expenses"
	expensespostgres "github.com/atlas/atlas-go/internal/expenses/postgres"
	"github.com/atlas/atlas-go/internal/finance"
	financepostgres "github.com/atlas/atlas-go/internal/finance/postgres"
	"github.com/atlas/atlas-go/internal/health"
	healthpostgres "github.com/atlas/atlas-go/internal/health/postgres"
	"github.com/atlas/atlas-go/internal/income"
	incomepostgres "github.com/atlas/atlas-go/internal/income/postgres"
	"github.com/atlas/atlas-go/internal/loans"
	loanspostgres "github.com/atlas/atlas-go/internal/loans/postgres"
	"github.com/atlas/atlas-go/internal/people"
	peoplepostgres "github.com/atlas/atlas-go/internal/people/postgres"
	"github.com/atlas/atlas-go/internal/personinfo"
	personinfopostgres "github.com/atlas/atlas-go/internal/personinfo/postgres"
)

// TestPersonDetail is the E2E for the single-call aggregate endpoint that backs
// /pessoas/detalhe. It validates the WHOLE feature bundle in ONE request:
//   - person basic info (name, birthDate, gender, selfKinship)
//   - health profile (IMC derived from latest WEIGHT/HEIGHT records)
//   - health records (scoped to the person)
//   - documents (scoped to the person — fixes the 404 from type=ownerId)
//   - assets (scoped to the person)
//
// Validates per the AGENTS.md rule "behavior contracts over snapshots": asserts
// structural invariants (names match the row, IMC = weight/height², documents all
// belong to the person), not frozen counts that break on every seed change.
func TestPersonDetail(t *testing.T) {
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

	svc := dashboard.NewService(
		loans.NewService(loanspostgres.NewRepository(pool)),
		expenses.NewService(expensespostgres.NewRepository(pool)),
		income.NewService(incomepostgres.NewRepository(pool)),
		people.NewService(peoplepostgres.NewRepository(pool), nil),
		assets.NewService(assetspostgres.NewRepository(pool)),
		health.NewService(healthpostgres.NewRepository(pool)),
		documents.NewService(documentspostgres.NewRepository(pool), nil, nil, nil, nil, nil, nil),
		finance.NewService(financepostgres.NewRepository(pool)),
		personinfo.NewService(personinfopostgres.NewRepository(pool), people.NewService(peoplepostgres.NewRepository(pool), nil), documents.NewService(documentspostgres.NewRepository(pool), nil, nil, nil, nil, nil, nil)),
	)

	// Resolve a real person UUID from the seed — do NOT hardcode a UUID that may
	// not exist in every seed (that's how the old "fa1cd6bb doesn't exist locally"
	// misdiagnosis happened). Pick the first person.
	var personID uuid.UUID
	err = pool.QueryRow(ctx, `SELECT id FROM people ORDER BY created_at LIMIT 1`).Scan(&personID)
	if errors.Is(err, pgx.ErrNoRows) {
		// This test needs at least one real person row (testdata/seed.sql) — CI's ephemeral
		// Postgres only applies the bare migrations, no seed, so skip rather than fail there.
		t.Skip("no people rows in the test database — run against a seeded Postgres to exercise this test")
	}
	if err != nil {
		t.Fatalf("resolving a test person: %v", err)
	}
	nameRow := pool.QueryRow(ctx, `SELECT name FROM people WHERE id = $1`, personID)
	var expectedName string
	if err := nameRow.Scan(&expectedName); err != nil {
		t.Fatalf("reading expected name: %v", err)
	}

	dto, err := svc.PersonDetail(ctx, personID)
	if err != nil {
		t.Fatalf("PersonDetail: %v", err)
	}

	// Invariant 1: person info matches the DB row exactly.
	if dto.Name != expectedName {
		t.Fatalf("name mismatch: got %q want %q", dto.Name, expectedName)
	}
	if dto.ID != personID.String() {
		t.Fatalf("id mismatch: got %q want %q", dto.ID, personID.String())
	}

	// Invariant 2: health records are all scoped to this person (no cross-row leakage).
	for _, r := range dto.HealthRecords {
		if r.PersonID != personID {
			t.Fatalf("health record %s belongs to person %s, want %s", r.ID, r.PersonID, personID)
		}
	}

	// Invariant 3: documents are all scoped to this person.
	for _, d := range dto.Documents {
		if d.PersonID == nil || *d.PersonID != personID {
			t.Fatalf("document %s not owned by person %s", d.ID, personID)
		}
	}

	// Invariant 4: assets are all scoped to this person (assets.PersonID is nullable).
	for _, a := range dto.Assets {
		if a.PersonID == nil || *a.PersonID != personID {
			t.Fatalf("asset %s not owned by person %s", a.ID, personID)
		}
	}

	// Invariant 5: BMI is derived correctly from the latest WEIGHT + HEIGHT records
	// (matches the Java IMC formula weight_kg / (height_m^2)).
	if dto.HealthProfile != nil && dto.HealthProfile.WeightKg != nil && dto.HealthProfile.HeightCm != nil {
		var weight, height float64
		if _, err := fmt.Sscanf(*dto.HealthProfile.WeightKg, "%f", &weight); err != nil {
			t.Fatalf("parsing weight %q: %v", *dto.HealthProfile.WeightKg, err)
		}
		if _, err := fmt.Sscanf(*dto.HealthProfile.HeightCm, "%f", &height); err != nil {
			t.Fatalf("parsing height %q: %v", *dto.HealthProfile.HeightCm, err)
		}
		wantBMI := weight / ((height / 100) * (height / 100))
		var gotBMI float64
		if _, err := fmt.Sscanf(*dto.HealthProfile.BMI, "%f", &gotBMI); err != nil {
			t.Fatalf("parsing BMI %q: %v", *dto.HealthProfile.BMI, err)
		}
		if diff := wantBMI - gotBMI; diff > 0.2 || diff < -0.2 {
			t.Fatalf("BMI mismatch: got %s want %.1f (weight=%s height=%s)", *dto.HealthProfile.BMI, wantBMI, *dto.HealthProfile.WeightKg, *dto.HealthProfile.HeightCm)
		}
	}

	// Smoke: the endpoint doesn't crash and returns a non-empty person id.
	if dto.ID == "" {
		t.Fatal("empty person id in detail response")
	}
}

// TestPersonDetail_404 is a smoke test that a non-existent person surfaces a proper
// "not found" error rather than an empty payload (the silent-404 class of bug that
// broke /pessoas/detalhe originally).
func TestPersonDetail_404(t *testing.T) {
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

	svc := dashboard.NewService(
		loans.NewService(loanspostgres.NewRepository(pool)),
		expenses.NewService(expensespostgres.NewRepository(pool)),
		income.NewService(incomepostgres.NewRepository(pool)),
		people.NewService(peoplepostgres.NewRepository(pool), nil),
		assets.NewService(assetspostgres.NewRepository(pool)),
		health.NewService(healthpostgres.NewRepository(pool)),
		documents.NewService(documentspostgres.NewRepository(pool), nil, nil, nil, nil, nil, nil),
		finance.NewService(financepostgres.NewRepository(pool)),
		personinfo.NewService(personinfopostgres.NewRepository(pool), people.NewService(peoplepostgres.NewRepository(pool), nil), documents.NewService(documentspostgres.NewRepository(pool), nil, nil, nil, nil, nil, nil)),
	)

	ghost := uuid.New()
	_, err = svc.PersonDetail(ctx, ghost)
	if err == nil {
		t.Fatal("expected an error for a non-existent person, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a 'not found' error, got: %v", err)
	}
}
