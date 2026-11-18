// Command api is the HTTP entrypoint (docs/architecture-plan.md directory tree). It is the
// composition root: the only place that imports both a domain package (e.g. internal/categories)
// and its postgres adapter (internal/categories/postgres) — see the comment in
// internal/categories/routes.go for why that wiring cannot live inside internal/categories itself
// without an import cycle.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/atlas/atlas-go/internal/academiceducation"
	academiceducationpostgres "github.com/atlas/atlas-go/internal/academiceducation/postgres"
	"github.com/atlas/atlas-go/internal/aiconnectors"
	aiconnectorspostgres "github.com/atlas/atlas-go/internal/aiconnectors/postgres"
	"github.com/atlas/atlas-go/internal/app"
	"github.com/atlas/atlas-go/internal/assets"
	assetspostgres "github.com/atlas/atlas-go/internal/assets/postgres"
	"github.com/atlas/atlas-go/internal/assistant"
	"github.com/atlas/atlas-go/internal/audit"
	auditpostgres "github.com/atlas/atlas-go/internal/audit/postgres"
	"github.com/atlas/atlas-go/internal/auditevents"
	auditeventspostgres "github.com/atlas/atlas-go/internal/auditevents/postgres"
	"github.com/atlas/atlas-go/internal/auth"
	"github.com/atlas/atlas-go/internal/backup"
	backuppostgres "github.com/atlas/atlas-go/internal/backup/postgres"
	"github.com/atlas/atlas-go/internal/calendar"
	calendarpostgres "github.com/atlas/atlas-go/internal/calendar/postgres"
	"github.com/atlas/atlas-go/internal/calendarsettings"
	calendarsettingspostgres "github.com/atlas/atlas-go/internal/calendarsettings/postgres"
	"github.com/atlas/atlas-go/internal/categories"
	categoriespostgres "github.com/atlas/atlas-go/internal/categories/postgres"
	"github.com/atlas/atlas-go/internal/chat"
	chatpostgres "github.com/atlas/atlas-go/internal/chat/postgres"
	"github.com/atlas/atlas-go/internal/clients"
	clientspostgres "github.com/atlas/atlas-go/internal/clients/postgres"
	"github.com/atlas/atlas-go/internal/config"
	"github.com/atlas/atlas-go/internal/controlmigrations"
	"github.com/atlas/atlas-go/internal/curator"
	curatorpostgres "github.com/atlas/atlas-go/internal/curator/postgres"
	"github.com/atlas/atlas-go/internal/dashboard"
	"github.com/atlas/atlas-go/internal/dashboardlayout"
	dashboardlayoutpostgres "github.com/atlas/atlas-go/internal/dashboardlayout/postgres"
	"github.com/atlas/atlas-go/internal/digitalaccounts"
	digitalaccountspostgres "github.com/atlas/atlas-go/internal/digitalaccounts/postgres"
	"github.com/atlas/atlas-go/internal/documents"
	documentspostgres "github.com/atlas/atlas-go/internal/documents/postgres"
	"github.com/atlas/atlas-go/internal/expenses"
	expensespostgres "github.com/atlas/atlas-go/internal/expenses/postgres"
	"github.com/atlas/atlas-go/internal/featureflags"
	"github.com/atlas/atlas-go/internal/finance"
	financepostgres "github.com/atlas/atlas-go/internal/finance/postgres"
	"github.com/atlas/atlas-go/internal/goals"
	goalspostgres "github.com/atlas/atlas-go/internal/goals/postgres"
	"github.com/atlas/atlas-go/internal/googlecal"
	"github.com/atlas/atlas-go/internal/health"
	healthpostgres "github.com/atlas/atlas-go/internal/health/postgres"
	"github.com/atlas/atlas-go/internal/icloudcal"
	"github.com/atlas/atlas-go/internal/income"
	incomepostgres "github.com/atlas/atlas-go/internal/income/postgres"
	"github.com/atlas/atlas-go/internal/integrations"
	integrationspostgres "github.com/atlas/atlas-go/internal/integrations/postgres"
	"github.com/atlas/atlas-go/internal/jobexperience"
	jobexperiencepostgres "github.com/atlas/atlas-go/internal/jobexperience/postgres"
	"github.com/atlas/atlas-go/internal/jobs"
	"github.com/atlas/atlas-go/internal/lifemapprefs"
	lifemapprefspostgres "github.com/atlas/atlas-go/internal/lifemapprefs/postgres"
	"github.com/atlas/atlas-go/internal/loans"
	loanspostgres "github.com/atlas/atlas-go/internal/loans/postgres"
	"github.com/atlas/atlas-go/internal/notifications"
	notificationspostgres "github.com/atlas/atlas-go/internal/notifications/postgres"
	"github.com/atlas/atlas-go/internal/openfinance"
	openfinancepostgres "github.com/atlas/atlas-go/internal/openfinance/postgres"
	"github.com/atlas/atlas-go/internal/pandora"
	"github.com/atlas/atlas-go/internal/passwordreset"
	passwordresetpostgres "github.com/atlas/atlas-go/internal/passwordreset/postgres"
	"github.com/atlas/atlas-go/internal/people"
	peoplepostgres "github.com/atlas/atlas-go/internal/people/postgres"
	"github.com/atlas/atlas-go/internal/personinfo"
	personinfopostgres "github.com/atlas/atlas-go/internal/personinfo/postgres"
	"github.com/atlas/atlas-go/internal/personrelations"
	"github.com/atlas/atlas-go/internal/pets"
	petspostgres "github.com/atlas/atlas-go/internal/pets/postgres"
	"github.com/atlas/atlas-go/internal/platform/captcha"
	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/platform/dbmigrate"
	"github.com/atlas/atlas-go/internal/platform/httpx"
	"github.com/atlas/atlas-go/internal/platform/keyring"
	"github.com/atlas/atlas-go/internal/platform/logging"
	"github.com/atlas/atlas-go/internal/platform/profiling"
	"github.com/atlas/atlas-go/internal/platform/secretbox"
	"github.com/atlas/atlas-go/internal/reports"
	"github.com/atlas/atlas-go/internal/settings"
	settingspostgres "github.com/atlas/atlas-go/internal/settings/postgres"
	"github.com/atlas/atlas-go/internal/spa"
	"github.com/atlas/atlas-go/internal/tenancy"
	"github.com/atlas/atlas-go/internal/tenantmigrations"
)

func main() {
	logger := logging.New()

	cfg, err := config.Load()
	if err != nil {
		logger.Error("startup: loading config", "cause", err.Error())
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Applies every pending migration (internal/migrations, embedded in the binary via go:embed)
	// before opening the pool the rest of the app uses — a deploy is self-contained (new image
	// boots, brings the schema up to date, then serves) instead of needing a manual `psql`/SSH
	// step run separately against production every time a migration is added. Fails fast: serving
	// traffic against a stale/partial schema is worse than not starting at all.
	if err := dbmigrate.UpTable(controlmigrations.FS, cfg.DatabaseURL, "schema_migrations_control"); err != nil {
		logger.Error("startup: applying control-plane migrations", "cause", err.Error())
		os.Exit(1)
	}

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, cfg.DBStatementTimeout)
	if err != nil {
		logger.Error("startup: opening database pool", "cause", err.Error())
		os.Exit(1)
	}
	defer pool.Close()

	// Tenant registry + key hierarchy, built early because per-tenant AI connector resolution (and
	// everything else tenant-scoped) depends on them.
	tenancyRegistry := tenancy.NewRegistry(pool)
	tenantKeyring, err := keyring.New(cfg.TenantMasterKey)
	if err != nil {
		logger.Error("startup: loading TENANT_MASTER_KEY", "cause", err.Error())
		os.Exit(1)
	}
	if !tenantKeyring.Configured() {
		logger.Warn("startup: TENANT_MASTER_KEY not set — tenant provisioning and tenant-key operations will fail")
	}
	tenantKeys := tenancy.NewKeyProvider(tenancyRegistry, tenantKeyring)
	defaultTenant, err := tenancyRegistry.EnsureDefault(ctx, cfg.TenantDefaultSlug, cfg.TenantDefaultSchema, "Atlas")
	if err != nil {
		logger.Error("startup: ensuring default tenant", "cause", err.Error())
		os.Exit(1)
	}

	// Composition root for the categories reference module — see the package doc on
	// internal/categories/routes.go for why this wiring lives here, not in internal/categories.
	categoriesRepo := categoriespostgres.NewRepository(pool)
	categoriesService := categories.NewService(categoriesRepo)
	categoriesHandler := categories.NewHandler(categoriesService)

	// Composition root for the clients module (loan-book counterparty). Owned by Claude (D-005);
	// wired here because cmd/api/main.go is the integration point (and Hermes-owned).
	clientsRepo := clientspostgres.NewRepository(pool)
	clientsService := clients.NewService(clientsRepo)
	clientsHandler := clients.NewHandler(clientsService)

	// Composition root for the loans module (loan book). Owned by Claude (D-005); wired here
	// because cmd/api/main.go is the integration point (and Hermes-owned).
	loansRepo := loanspostgres.NewRepository(pool)
	loansService := loans.NewService(loansRepo)
	loansHandler := loans.NewHandler(loansService)

	// Composition root for the expenses module. Owned by Claude (D-005); wired here (Hermes-owned
	// integration point). Already applies RequirePerm("expenses", ...) at the router level.
	expensesRepo := expensespostgres.NewRepository(pool)
	expensesService := expenses.NewService(expensesRepo)
	expensesHandler := expenses.NewHandler(expensesService)

	// Composition root for the income module. Owned by Claude (D-005); same wiring idiom.
	incomeRepo := incomepostgres.NewRepository(pool)
	incomeService := income.NewService(incomeRepo)
	incomeHandler := income.NewHandler(incomeService)

	// Composition root for the pets module. Owned by Claude (D-005); same wiring idiom.
	petsRepo := petspostgres.NewRepository(pool)
	petsService := pets.NewService(petsRepo)
	petsHandler := pets.NewHandler(petsService)

	// Composition root for the assets module. Owned by Claude (D-005); same wiring idiom.
	assetsRepo := assetspostgres.NewRepository(pool)
	assetsService := assets.NewService(assetsRepo)
	assetsHandler := assets.NewHandler(assetsService)

	// Composition root for the digitalaccounts module (inventory of external accounts/services —
	// GAP 1, docs/gap-analysis-vida-digital-20260822.md). No password/secret storage by design; see
	// internal/digitalaccounts/domain.go.
	digitalAccountsRepo := digitalaccountspostgres.NewRepository(pool)
	digitalAccountsService := digitalaccounts.NewService(digitalAccountsRepo)
	digitalAccountsHandler := digitalaccounts.NewHandler(digitalAccountsService)

	// Composition root for the audit module (generic edit-trail). Owned by Hermes (audit report
	// 2026-08-09, platform/infra row). Append-only: entries are created on change, never updated
	// or deleted.
	auditRepo := auditpostgres.NewRepository(pool)
	auditService := audit.NewService(auditRepo)
	auditHandler := audit.NewHandler(auditService)

	// Composition root for the audit-events module (security/exception trail — Configurações →
	// Auditoria). Separate from internal/audit above: this records logins, application errors,
	// panics and frontend exceptions into the control-plane audit_events table. The recorder is
	// async (RequestRecord never blocks a handler) and prunes events past cfg.AuditRetention
	// (default 3 days) so the table cannot grow without bound.
	auditEventsRepo := auditeventspostgres.NewRepository(pool)
	auditEventsService := auditevents.NewService(auditEventsRepo)
	auditEventsRecorder := auditevents.NewRecorder(auditEventsService, logger, cfg.AuditRetention)
	httpx.SetEventRecorder(auditEventsRecorder)
	auditEventsRecorder.Start(ctx)
	defer auditEventsRecorder.Close()
	auditEventsHandler := auditevents.NewHandler(auditEventsService)

	// Composition root for the backup module (backup_configuration: automatic backup schedule).
	// Hermes-owned (D-005, platform slice). CRUD over schedule rows.
	backupRepo := backuppostgres.NewRepository(pool)
	backupService := backup.NewService(backupRepo)
	backupHandler := backup.NewHandler(backupService)

	// Composition root for the chat module (per-user AI Atlas message persistence). Owned by Claude
	// (D-005); wired here (Hermes-owned). First module scoped per app_user_id (not shared household
	// data) — see internal/chat/domain.go.
	chatRepo := chatpostgres.NewRepository(pool)
	chatService := chat.NewService(chatRepo)
	chatHandler := chat.NewHandler(chatService)

	// Composition root for the dashboardlayout module (per-user custom dashboard widget
	// arrangement, saved from the "Editar layout" drag/resize UI). Scoped per app_user_id, like chat.
	dashboardLayoutService := dashboardlayout.NewService(dashboardlayoutpostgres.NewRepository(pool))
	dashboardLayoutHandler := dashboardlayout.NewHandler(dashboardLayoutService)

	// Composition root for the lifemapprefs module (per-user set of hidden Life Map area ids, saved
	// from the "Mapa da Vida" card's customization UI). Scoped per app_user_id, like dashboardlayout.
	lifeMapPrefsService := lifemapprefs.NewService(lifemapprefspostgres.NewRepository(pool))
	lifeMapPrefsHandler := lifemapprefs.NewHandler(lifeMapPrefsService)

	// Composition root for the finance module (financial_balances + append-only value history).
	// Owned by Claude.
	financeRepo := financepostgres.NewRepository(pool)
	financeService := finance.NewService(financeRepo)
	financeHandler := finance.NewHandler(financeService)

	goalsService := goals.NewService(goalspostgres.NewRepository(pool))
	goalsHandler := goals.NewHandler(goalsService)

	// Composition root for the calendar module (events the owner types). The dashboard composes it
	// too (WithCalendar below) so GET /api/dashboard/calendar serves derived + manual events in one
	// call.
	calendarRepo := calendarpostgres.NewRepository(pool)
	calendarService := calendar.NewService(calendarRepo)
	calendarHandler := calendar.NewHandler(calendarService)

	// Composition root for the integrations module (external/system credentials + AI config).
	// Owned by Claude (D-005). Connect/reconnect needs APP_ENCRYPTION_KEY in the env at
	// encrypt/decrypt time (not at boot) — see internal/integrations/crypto.go loadKey.
	// googleDriveOAuth is the household's OWN Drive-connect flow (distinct from documentsDrive
	// below, which only ever reads an already-connected credential) — redirectURL must be kept in
	// sync with whatever's authorized in Google Cloud Console (see
	// internal/integrations/googledrive_oauth.go).
	integrationsRepo := integrationspostgres.NewRepository(pool)
	googleDriveOAuth := integrations.NewGoogleDriveOAuthConfig(cfg.GoogleClientID, cfg.GoogleClientSecret,
		cfg.FrontendOrigin+"/api/integrations/google-drive/callback")
	googleCalendarOAuth := integrations.NewGoogleCalendarOAuthConfig(cfg.GoogleClientID, cfg.GoogleClientSecret,
		cfg.FrontendOrigin+"/api/integrations/google-calendar/callback")
	integrationsService := integrations.NewService(integrationsRepo, googleDriveOAuth).WithGoogleCalendar(googleCalendarOAuth)
	integrationsHandler := integrations.NewHandler(integrationsService, cfg.FrontendOrigin)

	// materializeRef is filled in once the dashboard service exists (it composes the module
	// services that produce the derived events); the sync services only call it lazily.
	var materializeRef func(context.Context) error
	materializeDerived := func(ctx context.Context) error {
		if materializeRef == nil {
			return nil
		}
		return materializeRef(ctx)
	}

	// Composition root for the iCloud Calendar (CalDAV) sync — the Apple half of the calendar
	// integration. Reuses calendarRepo for storage and integrationsService for the encrypted
	// credentials; the periodic run lives in cmd/worker.
	icloudCalendarService := icloudcal.NewService(icloudcal.Deps{
		Store:       calendarRepo,
		Credentials: integrationsService,
		Connector:   integrationsService,
		Materialize: materializeDerived,
		Logger:      logger,
	})
	icloudCalendarHandler := icloudcal.NewHandler(icloudCalendarService)

	// Composition root for the Google Calendar sync — the Google half, plus the global sync
	// destination (none/iCloud/Google) that decides which provider the worker runs.
	googleCalendarService := googlecal.NewService(googlecal.Deps{
		Store:       calendarRepo,
		Credentials: integrationsService,
		Connector:   integrationsService,
		Materialize: materializeDerived,
		Logger:      logger,
	})
	googleCalendarHandler := googlecal.NewHandler(googleCalendarService, cfg.FrontendOrigin)
	calendarSettingsService := calendarsettings.NewService(calendarsettingspostgres.NewRepository(pool))
	calendarSettingsHandler := calendarsettings.NewHandler(calendarSettingsService)

	// Composition root for the people + relationships module (family graph). The deriver is the
	// personrelations.BFS — injected here so the people package stays pure (no BFS impl imported).
	peopleRepo := peoplepostgres.NewRepository(pool)
	peopleService := people.NewService(peopleRepo, personrelations.DeriveFromOrigin)
	peopleHandler := people.NewHandler(peopleService)

	// Composition root for the settings module (app_settings singleton: company name, PIX key, logo).
	// Hermes-owned (D-005, platform slice). Get/Update only — no List/Create/Delete (single-row table).
	settingsRepo := settingspostgres.NewRepository(pool)
	settingsService := settings.NewService(settingsRepo)
	settingsHandler := settings.NewHandler(settingsService)

	// Composition root for the feature-flags module: an administrative proxy in front of the
	// feature-flag-mvp Control Plane (github.com/richardnogueira01/feature-flag-mvp — a separate
	// product, Atlas is just its first real consumer). Empty FeatureFlagControlPlaneURL means
	// "not configured": NewHTTPClient still builds cleanly, Client.Enabled() reports false, and
	// every route answers 404 instead of Atlas failing to start.
	featureFlagsClient := featureflags.NewHTTPClient(cfg.FeatureFlagControlPlaneURL)
	featureFlagsService := featureflags.NewService(featureFlagsClient)
	featureFlagsHandler := featureflags.NewHandler(featureFlagsService)

	// Composition root for the health module (MarkerCatalog + Record). Owned by Claude (D-005).
	// Built before documents: a LAB_RESULT document's auto-extraction (documents.Service.Extract)
	// writes into health_records, so documentsService needs healthService at construction time —
	// same reason expensesService/incomeService are built before documentsService below.
	healthRepo := healthpostgres.NewRepository(pool)
	healthService := health.NewService(healthRepo)
	healthHandler := health.NewHandler(healthService)

	// Composition root for the documents module (references assets + pets). Owned by Claude (D-005).
	// documentsDrive is the tenant's own Drive backend: new uploads are written to
	// Atlas/{Origem}/{arquivo} and read back through it; local disk (documentsStorage) is kept for
	// legacy rows and the one-time local→Drive migration (cmd/drive-migrate).
	documentsRepo := documentspostgres.NewRepository(pool)
	documentsStorage := documents.NewLocalStorage(cfg.UploadsDir, tenantKeys)
	documentsDrive := documents.NewGoogleDriveClient(cfg.GoogleClientID, cfg.GoogleClientSecret, integrationsService)
	// AI client: resolves the tenant's configured connector per call. No env fallback — the default
	// tenant's OmniRoute connector is seeded once below so the owner's AI keeps working.
	aiConnectorsRepo := aiconnectorspostgres.NewRepository(pool)
	pandoraService := pandora.NewService(aiconnectors.NewResolver(aiConnectorsRepo, tenantKeys, *defaultTenant))
	aiConnectorsService := aiconnectors.NewService(aiConnectorsRepo, tenantKeys)
	aiConnectorsHandler := aiconnectors.NewHandler(aiConnectorsService)
	// One-time seed: import the operator's OMNIROUTE_* env config as the default tenant's connector
	// (only if it has none yet). After this, AI config lives per tenant with no runtime fallback.
	if cfg.OmniRouteURL != "" {
		seedCtx := tenancy.WithTenantID(db.WithTenantSchema(ctx, defaultTenant.SchemaName), defaultTenant.ID)
		if existing, err := aiConnectorsService.List(seedCtx); err != nil {
			logger.Error("startup: listing AI connectors for seed", "cause", err.Error())
		} else if len(existing) == 0 {
			if _, err := aiConnectorsService.Create(seedCtx, aiconnectors.UpsertInput{
				Provider:        aiconnectors.ProviderOmniRoute,
				Label:           "OmniRoute (migrado do env)",
				BaseURL:         cfg.OmniRouteURL,
				APIKey:          cfg.OmniRouteAPIKey,
				ChatModel:       cfg.OmniRouteModel,
				ExtractionModel: cfg.OmniRouteExtractionModel,
				Priority:        100,
				Enabled:         true,
			}); err != nil {
				logger.Error("startup: seeding default AI connector", "cause", err.Error())
			} else {
				logger.Info("startup: seeded default tenant AI connector from env")
			}
		}
	}
	documentsService := documents.NewService(documentsRepo, documentsStorage, documentsDrive, pandoraService, expensesService, incomeService, healthService, categoriesService)
	documentsHandler := documents.NewHandler(documentsService)

	// Composition root for the personinfo module (pessoa_informacoes — RG/CPF/diploma/job facts
	// with edit history). References people + documents, so must come after both. Owned by Claude.
	personinfoRepo := personinfopostgres.NewRepository(pool)
	personinfoService := personinfo.NewService(personinfoRepo, peopleService, documentsService)
	personinfoHandler := personinfo.NewHandler(personinfoService)

	// Composition root for the jobexperience module (structured "Carreira" career history —
	// company/title/salary/period). References people + documents, so must come after both.
	jobexperienceRepo := jobexperiencepostgres.NewRepository(pool)
	jobexperienceService := jobexperience.NewService(jobexperienceRepo, peopleService, documentsService)
	jobexperienceHandler := jobexperience.NewHandler(jobexperienceService)

	// Composition root for the academiceducation module (structured "Educação" history —
	// institution/course/level/period), mirroring jobexperience's wiring exactly.
	academiceducationRepo := academiceducationpostgres.NewRepository(pool)
	academiceducationService := academiceducation.NewService(academiceducationRepo, peopleService, documentsService)
	academiceducationHandler := academiceducation.NewHandler(academiceducationService)

	// Composition root for the notifications module (Notification + singleton Configuration). Owned by Claude (D-005).
	notificationsRepo := notificationspostgres.NewRepository(pool)
	notificationsService := notifications.NewService(notificationsRepo)
	notificationsHandler := notifications.NewHandler(notificationsService)

	// Composition root for openfinance (Pluggy → Finanças). The API only stores the connection
	// (credentials encrypted via integrationsService), reads the synced data and queues a sync; the
	// actual syncing runs in cmd/worker. Credentials come from Configurações → Conexões, with the
	// PLUGGY_* environment variables as a headless fallback.
	openFinanceService := openfinance.NewService(openfinance.Deps{
		Repo:        openfinancepostgres.NewRepository(pool),
		Credentials: openfinance.ChainCredentials{integrationsService, openfinance.EnvCredentials{}},
		Connector:   integrationsService,
		Notifier:    notificationsService,
	})
	openFinanceHandler := openfinance.NewHandler(openFinanceService)

	// Composition root for the password-reset module (PASSWORD_RESET_TOKENS lifecycle). Hermes-owned
	// (carved out of auth per the task split, msg 91/94). PUBLIC: forgot/reset routes live on the
	// root mux OUTSIDE AuthMiddleware — a user resetting a password is by definition not logged in;
	// the token is the proof of identity. See B3 / reviews/claude-passwordreset-20260809.md.
	passwordResetRepo := passwordresetpostgres.NewRepository(pool)
	passwordResetService := passwordreset.NewService(passwordResetRepo)
	passwordResetHandler := passwordreset.NewHandler(passwordResetService)

	// Shared authz boundary (D-003, Hermes-owned). Built BEFORE RegisterRoutes so every module's
	// RequirePerm(httpx.DefaultCheckPerm, ...) captures a non-nil check function. OWNER bypasses the
	// check; MEMBER calls it — a nil DefaultCheckPerm would panic on the first MEMBER request (500).
	loadUser, checkPerm := auth.New(pool)
	httpx.DefaultCheckPerm = checkPerm

	// Real login (password + Google OAuth) — replaces the raw-user-id-as-bearer-token placeholder
	// AuthMiddleware used before real auth existed. Google is only registered when real credentials
	// are configured (GoogleOAuthConfig.Enabled(), checked inside RegisterPublicRoutes).
	authStore := auth.NewStore(pool)
	// secretBox encrypts each user's TOTP secret at rest; a nil box (no APP_ENCRYPTION_KEY) makes
	// MFA enrollment fail closed rather than store the secret in the clear.
	secretBox, err := secretbox.New(cfg.AppEncryptionKey)
	if err != nil {
		logger.Error("startup: building secret box", "cause", err.Error())
		os.Exit(1)
	}
	// Typed-nil guard: a nil *Turnstile stored in the Verifier interface would be non-nil and the
	// handler would call a method on a nil receiver. Keep the interface itself nil when unset.
	var captchaVerifier captcha.Verifier
	if cfg.TurnstileSecretKey != "" {
		captchaVerifier = captcha.NewTurnstile(cfg.TurnstileSecretKey)
	}
	googleOAuth := auth.NewGoogleOAuthConfig(cfg.GoogleClientID, cfg.GoogleClientSecret,
		cfg.FrontendOrigin+"/login/oauth2/code/google")
	authHandler := auth.NewHandler(authStore, googleOAuth, captchaVerifier, secretBox, cfg.MFATOTPIssuer, auditEventsRecorder)

	// Tenant self-service (docs/tenant-self-service.md). The default tenant is resolved above;
	// EnsureTenantKey/backfill/sweep below finish binding the owner's existing users and data.
	if err := tenantKeys.EnsureTenantKey(ctx, defaultTenant.ID); err != nil {
		logger.Error("startup: ensuring default tenant DEK", "cause", err.Error())
		os.Exit(1)
	}
	if n, err := tenancyRegistry.AssignUsersWithoutTenant(ctx, defaultTenant.ID); err != nil {
		logger.Error("startup: backfilling app_users.tenant_id", "cause", err.Error())
		os.Exit(1)
	} else if n > 0 {
		logger.Info("startup: bound existing users to default tenant", "count", n, "schema", defaultTenant.SchemaName)
	}
	// Migrate every existing tenant schema: the owner's dedicated schema after the cutover, and
	// every self-service tenant. `public` is skipped — it holds control tables (tracked by
	// controlmigrations) and is the owner's domain schema only until the cutover.
	tenantMigrator := tenancy.SchemaMigratorFrom(tenantmigrations.FS, cfg.DatabaseURL)
	allTenants, err := tenancyRegistry.List(ctx)
	if err != nil {
		logger.Error("startup: listing tenants for schema migration", "cause", err.Error())
		os.Exit(1)
	}
	for _, t := range allTenants {
		if t.SchemaName == "" || t.SchemaName == "public" {
			continue
		}
		if err := tenantMigrator(ctx, t.SchemaName); err != nil {
			logger.Error("startup: migrating tenant schema", "tenant", t.Slug, "schema", t.SchemaName, "cause", err.Error())
			os.Exit(1)
		}
	}
	// Background tasks in this process (calendar sync, the in-process reports runner) are not
	// request-scoped and therefore carry no tenant schema. Bind them to the default tenant for now;
	// running them per tenant is the multi-tenant follow-up.
	bgCtx := ctx
	if defaultTenant != nil {
		bgCtx = db.WithTenantSchema(ctx, defaultTenant.SchemaName)
	}
	// Extractions killed by the previous process's shutdown would stay PENDING forever.
	for _, t := range allTenants {
		if t.SchemaName == "" || t.SchemaName == "public" {
			continue
		}
		sweepCtx := tenancy.WithTenantID(db.WithTenantSchema(ctx, t.SchemaName), t.ID)
		if n, err := documentsService.FailInterruptedExtractions(sweepCtx); err != nil {
			logger.Error("startup: failing interrupted extractions", "tenant", t.Slug, "cause", err.Error())
		} else if n > 0 {
			logger.Info("startup: marked interrupted extractions as failed", "tenant", t.Slug, "count", n)
		}
	}
	// One-time (idempotent) sweep: bring any pre-encryption uploads of the owner under their DEK.
	if defaultTenant != nil {
		sweepCtx := tenancy.WithTenantID(db.WithTenantSchema(ctx, defaultTenant.SchemaName), defaultTenant.ID)
		if n, err := documentsService.EncryptLegacyFiles(sweepCtx); err != nil {
			logger.Error("startup: encrypting legacy uploads", "cause", err.Error())
		} else if n > 0 {
			logger.Info("startup: encrypted legacy uploads", "count", n, "tenant", defaultTenant.Slug)
		}
	}
	tenancyProvisioner := tenancy.NewProvisioner(pool, tenancyRegistry, tenantMigrator, tenantKeyring)
	tenancyHandler := tenancy.NewHandler(tenancyProvisioner, authStore, tenancy.Config{
		Enabled:     cfg.SignupEnabled,
		InviteToken: cfg.SignupInviteToken,
	})

	// IA Atlas (internal/ai) desligada — o antigo código Java-era permanece no repo mas não é mais
	// registrado. A camada de orquestração real é internal/assistant, abaixo, que fala com o
	// OmniRoute via internal/pandora.

	// Composition root for the pandora module — proxy to OmniRoute (OMNIROUTE_URL/API_KEY/MODEL,
	// see internal/config). Only the client-facing chat path remains; no admin/config surface.
	// The public dashboard-chat route is token-gated and rate-limited (pandora.DashboardConfig /
	// RegisterPublicRoutes).
	pandoraHandler := pandora.NewHandler(pandoraService, pandora.DashboardConfig{
		Token:          cfg.DashboardChatToken,
		AllowedOrigins: cfg.DashboardChatAllowedOrigin,
	})

	// Composition root for the assistant module ("IA Atlas") — the tool-calling orchestration
	// layer internal/chat's own doc comment anticipated. Read tools over the financial modules
	// (loans, expenses, income, balances, categories), family, pets and health; write tools pause
	// for user confirmation. See internal/assistant/domain.go.
	assistantService := assistant.NewService(chatService, pandoraService, loansService, peopleService, petsService, healthService, expensesService, incomeService, financeService, goalsService, categoriesService)
	assistantHandler := assistant.NewHandler(assistantService)

	// Composition root for the dashboard module — composes the other modules' own services (no
	// repository of its own); see internal/dashboard/service.go's package doc for what it still
	// leaves out (insights/recommendations, per-person health profiles).
	curatorRepo := curatorpostgres.NewRepository(pool)
	curatorService := curator.NewService(curatorRepo, pandoraService, expensesService, incomeService, categoriesService, loansService, clientsService, healthService, financeService, peopleService, goalsService)
	dashboardService := dashboard.NewService(loansService, expensesService, incomeService, peopleService, assetsService, healthService, documentsService, financeService, personinfoService, curatorService).
		WithPets(petsService).
		WithCalendar(calendarService).
		WithInvestments(openFinanceService)
	dashboardHandler := dashboard.NewHandler(dashboardService)

	// Derived events (instalments, birthdays, document expirations) are materialized as read-only
	// calendar rows before every sync, so the provider mirrors the Atlas calendar.
	materializeRef = func(ctx context.Context) error {
		now := time.Now()
		items := dashboardService.DerivedEvents(ctx, now.AddDate(0, 0, -90), now.AddDate(0, 0, 730))
		return calendarService.ReplaceDerived(ctx, items)
	}

	// Composition root for the reports module (client loan statement PDF, legacy spreadsheet
	// export, combined financial PDF) — internal/jobs' durable queue (jobs table, lease-based crash
	// recovery) backs it; runner.Start below is the in-process "fast path" worker pool (the analog
	// of Java's @Async dispatch), cmd/worker's Picker is the safety net for anything the fast path
	// loses (process restart between job creation and dispatch). Owned by Claude (D-005).
	jobsRepo := jobs.NewPostgresRepository(pool)
	reportsStorage := reports.NewStorage(cfg.UploadsDir)
	reportsService := reports.NewService(jobsRepo, reportsStorage, clientsService, loansService, expensesService, incomeService, categoriesService, peopleService, healthService, personinfoService, settingsRepo)
	reportsRunner := jobs.NewRunner(jobsRepo, reportsService.Process, logger, 2, 32)
	reportsService.SetRunner(reportsRunner)
	reportsRunner.Start(bgCtx)
	reportsHandler := reports.NewHandler(reportsService)

	mux := http.NewServeMux()
	auth.RegisterAuthenticatedRoutes(mux, authHandler)
	dashboard.RegisterRoutes(mux, dashboardHandler)
	categories.RegisterRoutes(mux, categoriesHandler)
	people.RegisterRoutes(mux, peopleHandler)
	personinfo.RegisterRoutes(mux, personinfoHandler)
	jobexperience.RegisterRoutes(mux, jobexperienceHandler)
	academiceducation.RegisterRoutes(mux, academiceducationHandler)
	clients.RegisterRoutes(mux, clientsHandler)
	loans.RegisterRoutes(mux, loansHandler)
	expenses.RegisterRoutes(mux, expensesHandler)
	income.RegisterRoutes(mux, incomeHandler)
	pets.RegisterRoutes(mux, petsHandler)
	assets.RegisterRoutes(mux, assetsHandler)
	digitalaccounts.RegisterRoutes(mux, digitalAccountsHandler)
	audit.RegisterRoutes(mux, auditHandler)
	auditevents.RegisterRoutes(mux, auditEventsHandler)
	backup.RegisterRoutes(mux, backupHandler)
	documents.RegisterRoutes(mux, documentsHandler)
	health.RegisterRoutes(mux, healthHandler)
	notifications.RegisterRoutes(mux, notificationsHandler)
	settings.RegisterRoutes(mux, settingsHandler)
	aiconnectors.RegisterRoutes(mux, aiConnectorsHandler)
	featureflags.RegisterRoutes(mux, featureFlagsHandler)
	chat.RegisterRoutes(mux, chatHandler)
	dashboardlayout.RegisterRoutes(mux, dashboardLayoutHandler)
	lifemapprefs.RegisterRoutes(mux, lifeMapPrefsHandler)
	pandora.RegisterRoutes(mux, pandoraHandler)
	assistant.RegisterRoutes(mux, assistantHandler)
	finance.RegisterRoutes(mux, financeHandler)
	goals.RegisterRoutes(mux, goalsHandler)
	calendar.RegisterRoutes(mux, calendarHandler)
	integrations.RegisterRoutes(mux, integrationsHandler)
	icloudcal.RegisterRoutes(mux, icloudCalendarHandler)
	googlecal.RegisterRoutes(mux, googleCalendarHandler)
	calendarsettings.RegisterRoutes(mux, calendarSettingsHandler)
	openfinance.RegisterRoutes(mux, openFinanceHandler)
	reports.RegisterRoutes(mux, reportsHandler)

	// Liveness probe (B3 finding, Hermes-owned): GET /health must be reachable WITHOUT a token,
	// otherwise dashboards/orchestrators can't probe the API. AuthMiddleware therefore wraps only
	// the module mux; the root mux exposes /health outside it. Recover+AccessLog stay outermost.
	root := http.NewServeMux()
	root.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	root.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not_ready"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	// "/api/" (not "/") is the pattern AuthMiddleware wraps — every module route is "/api/...", so
	// this is exactly as protective as wrapping "/" was, but leaves the frontend's static export
	// (registered on "/" below) reachable without a session, matching Java's SpaWebConfig: the SPA
	// shell has to load before a client can even attempt to log in.
	root.Handle("/api/", httpx.AuthMiddleware(loadUser)(tenancy.WithTenantSchema(mux)))
	root.Handle("/", spa.NewHandler(cfg.StaticDir))

	// Public (unauthenticated) module routes: POST /api/auth/forgot-password and /reset-password.
	// Registered on the root mux so AuthMiddleware never sees them (see internal/passwordreset/routes.go).
	passwordreset.RegisterRoutes(root, passwordResetHandler)

	// Public (unauthenticated) auth routes: POST /api/auth/login, /logout, and (if configured)
	// GET /oauth2/authorization/google + /login/oauth2/code/google. Same reasoning as passwordreset
	// above — these routes exist to CREATE a session, so they can't require one already existing.
	auth.RegisterPublicRoutes(root, authHandler)

	// Public (unauthenticated) self-service signup: creates a new tenant schema + its OWNER user.
	// Registered only when SIGNUP_ENABLED (see tenancy.RegisterPublicRoutes).
	tenancy.RegisterPublicRoutes(root, tenancyHandler)

	// Public (unauthenticated), CORS-enabled: POST /api/public/dashboard-chat — the external
	// dashboard's own chat widget, a different origin with no atlas-go session. See
	// internal/pandora/routes.go's RegisterPublicRoutes doc comment for why.
	pandora.RegisterPublicRoutes(root, pandoraHandler)

	handler := app.NewRouter(root, logger, cfg.FrontendOrigin)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	var profilingServer *http.Server
	if cfg.PprofAddr != "" {
		profilingServer = &http.Server{Addr: cfg.PprofAddr, Handler: profiling.NewHandler()}
		go func() {
			logger.Info("pprof starting", "addr", cfg.PprofAddr)
			if err := profilingServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("pprof: server error", "cause", err.Error())
			}
		}()
	}

	go func() {
		syncCalendar := func() {
			target, err := calendarSettingsService.Get(bgCtx)
			if err != nil {
				logger.Error("calendar sync: target lookup failed", "cause", err.Error())
				return
			}
			switch target {
			case calendarsettings.TargetICloud:
				icloudCalendarService.Tick(bgCtx)
			case calendarsettings.TargetGoogle:
				googleCalendarService.Tick(bgCtx)
			}
		}
		syncCalendar()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				syncCalendar()
			}
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown: forced close", "cause", err.Error())
		}
		if profilingServer != nil {
			if err := profilingServer.Shutdown(shutdownCtx); err != nil {
				logger.Error("pprof: shutdown failed", "cause", err.Error())
			}
		}
	}()

	logger.Info("api starting", "port", cfg.Port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("api: server error", "cause", err.Error())
		os.Exit(1)
	}
	logger.Info("api stopped")
}
