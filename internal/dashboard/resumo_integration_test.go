//go:build integration

package dashboard_test

import (
	"context"
	"os"
	"testing"

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

// TestResumoCurrentLoansNeverNil reproduz o bug real observado em produção local: quando não há
// nenhum empréstimo vigente no mês corrente, `vigentesByMonth[currentMonth]` devolve o zero-value
// do map (nil slice), e um `[]T` nil serializa como `null` em JSON — o frontend então quebra com
// "Cannot read properties of null (reading 'map')" ao processar dto.currentLoans. O handler
// internal/loans/handler.go's Vigentes já tratava esse mesmo caso; Resumo não tratava.
func TestResumoCurrentLoansNeverNil(t *testing.T) {
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

	dto, err := svc.Resumo(ctx, 1)
	if err != nil {
		t.Fatalf("Resumo: %v", err)
	}

	if dto.CurrentLoans == nil {
		t.Fatal("CurrentLoans is nil — serializes as JSON null and breaks the frontend's .map() over dto.currentLoans")
	}
	if dto.RecentActivity == nil {
		t.Fatal("RecentActivity is nil — serializes as JSON null and breaks the frontend's .map() over dto.recentActivity")
	}
	if dto.Insights == nil {
		t.Fatal("Insights is nil — serializes as JSON null and breaks the frontend's .map() over dto.insights")
	}
	if dto.Recommendations == nil {
		t.Fatal("Recommendations is nil — serializes as JSON null and breaks the frontend's .map() over dto.recommendations")
	}
}
