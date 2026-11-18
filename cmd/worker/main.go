// Command worker is the background job runner (Hermes-owned, D-005 platform slice). It drives the
// periodic operations that have no HTTP surface: the RECURRING_INTEREST daily rollover (loans.Service
// method provided by Claude per the task split) and the backup schedule from backup_configuration.
//
// Design: a single time.Ticker fires every minute; on each tick we evaluate what's due:
//   - rollover: once per day (guarded by last-run date)
//   - backup: when the current local hour:minute matches any backup_configuration row — runs a
//     real pg_dump (-F c) into BackupsDir, prunes anything past the newest 14, and records an
//     in-app BACKUP_COMPLETED/BACKUP_FAILED notification either way
//
// This keeps the wiring dependency-free (no external cron) and single-tenant-friendly. This
// binary must actually be running for any of the above to happen — see Dockerfile/docker-compose
// for how it's deployed alongside cmd/api, not instead of it.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/aiconnectors"
	aiconnectorspostgres "github.com/atlas/atlas-go/internal/aiconnectors/postgres"
	backuppostgres "github.com/atlas/atlas-go/internal/backup/postgres"
	"github.com/atlas/atlas-go/internal/categories"
	categoriespostgres "github.com/atlas/atlas-go/internal/categories/postgres"
	"github.com/atlas/atlas-go/internal/clients"
	clientspostgres "github.com/atlas/atlas-go/internal/clients/postgres"
	"github.com/atlas/atlas-go/internal/config"
	"github.com/atlas/atlas-go/internal/curator"
	curatorpostgres "github.com/atlas/atlas-go/internal/curator/postgres"
	"github.com/atlas/atlas-go/internal/documents"
	documentspostgres "github.com/atlas/atlas-go/internal/documents/postgres"
	"github.com/atlas/atlas-go/internal/expenses"
	expensespostgres "github.com/atlas/atlas-go/internal/expenses/postgres"
	"github.com/atlas/atlas-go/internal/finance"
	financepostgres "github.com/atlas/atlas-go/internal/finance/postgres"
	"github.com/atlas/atlas-go/internal/goals"
	goalspostgres "github.com/atlas/atlas-go/internal/goals/postgres"
	"github.com/atlas/atlas-go/internal/health"
	healthpostgres "github.com/atlas/atlas-go/internal/health/postgres"
	"github.com/atlas/atlas-go/internal/income"
	incomepostgres "github.com/atlas/atlas-go/internal/income/postgres"
	"github.com/atlas/atlas-go/internal/integrations"
	integrationspostgres "github.com/atlas/atlas-go/internal/integrations/postgres"
	"github.com/atlas/atlas-go/internal/jobs"
	"github.com/atlas/atlas-go/internal/loans"
	loanspostgres "github.com/atlas/atlas-go/internal/loans/postgres"
	"github.com/atlas/atlas-go/internal/notifications"
	notificationspostgres "github.com/atlas/atlas-go/internal/notifications/postgres"
	"github.com/atlas/atlas-go/internal/openfinance"
	openfinancepostgres "github.com/atlas/atlas-go/internal/openfinance/postgres"
	"github.com/atlas/atlas-go/internal/pandora"
	"github.com/atlas/atlas-go/internal/people"
	peoplepostgres "github.com/atlas/atlas-go/internal/people/postgres"
	"github.com/atlas/atlas-go/internal/personrelations"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/atlas/atlas-go/internal/platform/db"
	"github.com/atlas/atlas-go/internal/platform/keyring"
	"github.com/atlas/atlas-go/internal/platform/logging"
	"github.com/atlas/atlas-go/internal/reports"
	settingspostgres "github.com/atlas/atlas-go/internal/settings/postgres"
	"github.com/atlas/atlas-go/internal/tenancy"
)

func main() {
	logger := logging.New()

	cfg, err := config.Load()
	if err != nil {
		logger.Error("worker startup: loading config", "cause", err.Error())
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, cfg.DBStatementTimeout)
	if err != nil {
		logger.Error("worker startup: opening database pool", "cause", err.Error())
		os.Exit(1)
	}
	defer pool.Close()

	// Multi-tenant worker: every scheduled task runs once per registered tenant with a
	// tenant-scoped context, so db.WithTx's transaction-scoped search_path lands on the right
	// schema. The tenant list is re-read on every tick, so tenants created by self-service signup
	// start being served without a worker restart.
	tenancyRegistry := tenancy.NewRegistry(pool)
	listTenants := func(ctx context.Context) []tenancy.Tenant {
		tenants, err := tenancyRegistry.List(ctx)
		if err != nil {
			logger.Error("worker: listing tenants", "cause", err.Error())
			return nil
		}
		return tenants
	}
	tenantCtx := func(ctx context.Context, t tenancy.Tenant) context.Context {
		return db.WithTenantSchema(ctx, t.SchemaName)
	}
	tenantKeyring, err := keyring.New(cfg.TenantMasterKey)
	if err != nil {
		logger.Error("worker startup: loading TENANT_MASTER_KEY", "cause", err.Error())
		os.Exit(1)
	}
	tenantKeys := tenancy.NewKeyProvider(tenancyRegistry, tenantKeyring)
	defaultTenant, err := tenancyRegistry.BySlug(ctx, cfg.TenantDefaultSlug)
	if err != nil {
		logger.Error("worker startup: resolving default tenant", "cause", err.Error())
		os.Exit(1)
	}
	var defaultTenantValue tenancy.Tenant
	if defaultTenant != nil {
		defaultTenantValue = *defaultTenant
	}

	// backupRecipient is nil when APP_BACKUP_AGE_RECIPIENT isn't set — runPgDump falls back to
	// writing a plain, unencrypted dump in that case (see config.Config.BackupAgeRecipient's doc
	// comment on why this degrades instead of refusing to start).
	var backupRecipient age.Recipient
	if cfg.BackupAgeRecipient != "" {
		r, err := age.ParseX25519Recipient(cfg.BackupAgeRecipient)
		if err != nil {
			logger.Error("worker startup: invalid APP_BACKUP_AGE_RECIPIENT, backups will NOT be encrypted", "cause", err.Error())
		} else {
			backupRecipient = r
		}
	} else {
		logger.Warn("worker startup: APP_BACKUP_AGE_RECIPIENT not set — backups will be written unencrypted")
	}

	// Rollover wiring (Claude's loans.Service.RolloverRecurringInterest — pure method, no ticker).
	loansRepo := loanspostgres.NewRepository(pool)
	loansService := loans.NewService(loansRepo)

	// Optional daily maintenance (overdue sweep + due-soon alert), needs notifications.
	notificationsRepo := notificationspostgres.NewRepository(pool)
	notificationsService := notifications.NewService(notificationsRepo)
	maintenance := loans.NewMaintenanceService(loansRepo, notificationsService)
	documentsRepo := documentspostgres.NewRepository(pool)
	deadlineService := documents.NewDeadlineService(documentsRepo, notificationsService)

	// Backup schedule source.
	backupRepo := backuppostgres.NewRepository(pool)

	// Reports job picker — the safety net for internal/reports' fast-path dispatch (cmd/api's
	// runner.Start): reclaims any job whose fast-path dispatch was lost (e.g. the API process
	// restarted between job creation and dispatch) or whose worker died mid-processing (expired
	// lease). See internal/jobs/picker.go.
	jobsRepo := jobs.NewPostgresRepository(pool)
	clientsRepo := clientspostgres.NewRepository(pool)
	clientsService := clients.NewService(clientsRepo)
	expensesRepo := expensespostgres.NewRepository(pool)
	expensesService := expenses.NewService(expensesRepo)
	incomeRepo := incomepostgres.NewRepository(pool)
	incomeService := income.NewService(incomeRepo)
	categoriesService := categories.NewService(categoriespostgres.NewRepository(pool))
	healthService := health.NewService(healthpostgres.NewRepository(pool))
	financeService := finance.NewService(financepostgres.NewRepository(pool))
	goalsService := goals.NewService(goalspostgres.NewRepository(pool))
	// Open Finance (Pluggy) sync. Credentials come from Configurações → Conexões (needs the same
	// APP_ENCRYPTION_KEY as the API), with PLUGGY_* env vars as a headless fallback. With neither,
	// Tick is a no-op, so an install that doesn't use Pluggy pays nothing for this.
	integrationsService := integrations.NewService(integrationspostgres.NewRepository(pool), integrations.NewGoogleDriveOAuthConfig("", "", ""))
	openFinanceService := openfinance.NewService(openfinance.Deps{
		Repo:        openfinancepostgres.NewRepository(pool),
		Credentials: openfinance.ChainCredentials{integrationsService, openfinance.EnvCredentials{}},
		Notifier:    notificationsService,
		Logger:      logger,
		Interval:    pluggySyncInterval(logger),
	})

	peopleService := people.NewService(peoplepostgres.NewRepository(pool), personrelations.DeriveFromOrigin)

	pandoraService := pandora.NewService(aiconnectors.NewResolver(aiconnectorspostgres.NewRepository(pool), tenantKeys, defaultTenantValue))
	curatorService := curator.NewService(curatorpostgres.NewRepository(pool), pandoraService,
		expensesService, incomeService, categoriesService, loansService, clientsService, healthService,
		financeService, peopleService, goalsService)
	settingsRepo := settingspostgres.NewRepository(pool)
	reportsStorage := reports.NewStorage(cfg.UploadsDir)
	// categories/people/health/personinfo are nil here: the worker only ever drives
	// reportsService.Process (the async job dispatch — loan statement/spreadsheet/financial jobs),
	// never the synchronous expense/income/emergency-card handlers that need them, so there's
	// nothing to wire for this process.
	reportsService := reports.NewService(jobsRepo, reportsStorage, clientsService, loansService, expensesService, incomeService, nil, nil, nil, nil, settingsRepo)
	reportsRunner := jobs.NewRunner(jobsRepo, reportsService.Process, logger, 2, 32)
	reportsService.SetRunner(reportsRunner)
	reportsRunner.Start(ctx)
	// One picker per tenant: the picker's ctx carries the tenant schema, and Runner.Dispatch
	// re-attaches it to the job. Started lazily and re-checked every tick so new tenants are
	// picked up without a restart.
	var pickersMu sync.Mutex
	startedPickers := map[string]bool{}
	startPickerFor := func(t tenancy.Tenant) {
		if t.SchemaName == "" {
			return
		}
		pickersMu.Lock()
		defer pickersMu.Unlock()
		if startedPickers[t.Slug] {
			return
		}
		startedPickers[t.Slug] = true
		p := jobs.NewPicker(jobsRepo, reportsRunner, logger, 30*time.Second, 5*time.Minute)
		go p.Run(tenantCtx(ctx, t))
		logger.Info("worker: job picker started", "tenant", t.Slug, "schema", t.SchemaName)
	}

	var (
		mu                 sync.Mutex
		lastRollover       = map[string]string{} // slug -> YYYY-MM-DD of the last successful rollover
		lastCuratorAttempt = map[string]time.Time{}
	)

	runRollover := func() {
		mu.Lock()
		defer mu.Unlock()
		// civildate.Location(), not the container's own local zone: a container running in UTC
		// (the default with no TZ set) rolls its date string over up to 3h before São Paulo does,
		// which could skip or double-run this dedupe-by-date-string check right at the boundary —
		// same class of bug civildate.Today's doc comment describes for the maintenance sweep.
		today := time.Now().In(civildate.Location()).Format("2006-01-02")
		for _, t := range listTenants(ctx) {
			if t.SchemaName == "" || lastRollover[t.Slug] == today {
				continue
			}
			tctx := tenantCtx(ctx, t)
			if err := loansService.RolloverRecurringInterest(tctx, time.Now()); err != nil {
				logger.Error("worker: rollover failed", "tenant", t.Slug, "cause", err.Error())
				continue
			}
			report, err := maintenance.RunDailyMaintenance(tctx)
			if err != nil {
				logger.Error("worker: daily maintenance failed", "tenant", t.Slug, "cause", err.Error())
				continue
			}
			deadlineAlerts, err := deadlineService.Run(tctx, time.Now().In(civildate.Location()))
			if err != nil {
				logger.Error("worker: document deadline alerts failed", "tenant", t.Slug, "cause", err.Error())
				continue
			}
			lastRollover[t.Slug] = today
			logger.Info("worker: rollover + maintenance completed", "tenant", t.Slug, "date", today,
				"installmentsMarkedOverdue", report.InstallmentsMarkedOverdue,
				"dueSoonAlertsSent", report.DueSoonAlertsSent,
				"documentDeadlineAlertsSent", deadlineAlerts)
		}
	}

	runCurator := func() {
		// The curator is optional: without an OmniRoute endpoint, skip the job instead of
		// emitting the same configuration error every minute.
		if cfg.OmniRouteURL == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		for _, t := range listTenants(ctx) {
			if t.SchemaName == "" {
				continue
			}
			if last := lastCuratorAttempt[t.Slug]; !last.IsZero() && now.Sub(last) < time.Hour {
				continue
			}
			lastCuratorAttempt[t.Slug] = now
			report, err := curatorService.RunDaily(tenantCtx(ctx, t), civildate.Today().Time())
			if err != nil {
				logger.Error("worker: daily AI curator failed", "tenant", t.Slug, "cause", err.Error())
				continue
			}
			if !report.Skipped {
				logger.Info("worker: daily AI curator completed", "tenant", t.Slug, "reclassified", report.Reclassified, "insights", report.Insights)
			}
		}
	}

	runOpenFinance := func() {
		// Serialised with the other jobs (mu) so a long first sync can't overlap a backup.
		mu.Lock()
		defer mu.Unlock()
		for _, t := range listTenants(ctx) {
			if t.SchemaName == "" {
				continue
			}
			if _, err := openFinanceService.Tick(tenantCtx(ctx, t)); err != nil {
				logger.Error("worker: open finance sync failed", "tenant", t.Slug, "cause", err.Error())
			}
		}
	}

	// backupRetentionCount bounds disk usage — pg_dump's custom format is already compressed, so a
	// daily cadence over this many days is a small, predictable footprint without needing a
	// separate retention config surface (backup_configuration only ever asked "when", not "how
	// many to keep").
	const backupRetentionCount = 14

	runBackup := func() {
		// civildate.Location(): backup_configuration's hour/minute are entered by the household
		// through the settings UI in their own wall-clock time, not UTC — comparing against the
		// container's bare local time would fire up to 3h off from what was actually configured.
		now := time.Now().In(civildate.Location())
		// Per tenant: each tenant has its own backup_configuration schedule and gets its own
		// notification. runPgDump still dumps the whole database (all tenant schemas), so two
		// tenants sharing a schedule produce two whole-DB dumps — acceptable while the homelab
		// CronJob remains the primary backup; per-tenant `pg_dump -n <schema>` is a follow-up.
		for _, t := range listTenants(ctx) {
			if t.SchemaName == "" {
				continue
			}
			tctx := tenantCtx(ctx, t)
			cfgs, err := backupRepo.List(tctx)
			if err != nil {
				logger.Error("worker: backup list failed", "tenant", t.Slug, "cause", err.Error())
				continue
			}
			for i := range cfgs {
				c := &cfgs[i]
				if c.Hour != now.Hour() || c.Minute != now.Minute() {
					continue
				}
				logger.Info("worker: backup due", "tenant", t.Slug, "config_id", c.ID, "scheduled", fmt.Sprintf("%02d:%02d", c.Hour, c.Minute))
				filename, size, err := runPgDump(tctx, cfg.DatabaseURL, cfg.BackupsDir, backupRecipient)
				if err != nil {
					logger.Error("worker: backup failed", "tenant", t.Slug, "config_id", c.ID, "cause", err.Error())
					if _, nerr := notificationsService.Create(tctx, notifications.CreateNotificationRequest{
						Type:    notifications.NotificationTypeBackupFailed,
						Title:   "Backup automático falhou",
						Message: fmt.Sprintf("O backup agendado das %02d:%02d falhou: %s", c.Hour, c.Minute, err.Error()),
					}); nerr != nil {
						logger.Error("worker: failed to record backup-failed notification", "cause", nerr.Error())
					}
					continue
				}
				pruneOldBackups(cfg.BackupsDir, backupRetentionCount, logger)
				if err := recordBackupRun(tctx, pool, c.ID); err != nil {
					logger.Error("worker: failed to stamp last_automatic_backup_at", "cause", err.Error())
				}
				if _, nerr := notificationsService.Create(tctx, notifications.CreateNotificationRequest{
					Type:    notifications.NotificationTypeBackupCompleted,
					Title:   "Backup automático concluído",
					Message: fmt.Sprintf("Backup salvo em %s (%s).", filename, humanizeBytes(size)),
				}); nerr != nil {
					logger.Error("worker: failed to record backup-completed notification", "cause", nerr.Error())
				}
				logger.Info("worker: backup completed", "tenant", t.Slug, "config_id", c.ID, "file", filename, "bytes", size)

				// Consolidated restic sweep (/DATA — replaces the old backup-homelab.sh systemd timer,
				// see the DR audit this was built from), riding the same backup_configuration schedule
				// as the pg_dump above. Skipped entirely when RESTIC_REPOSITORY isn't set, same
				// degrade-gracefully idiom as backupRecipient.
				if cfg.ResticRepository != "" {
					if err := runResticSweep(tctx, cfg.ResticRepository, cfg.ResticPasswordFile, cfg.ResticDataDir); err != nil {
						logger.Error("worker: restic sweep failed", "tenant", t.Slug, "config_id", c.ID, "cause", err.Error())
						if _, nerr := notificationsService.Create(tctx, notifications.CreateNotificationRequest{
							Type:    notifications.NotificationTypeBackupFailed,
							Title:   "Backup geral (restic) falhou",
							Message: fmt.Sprintf("O sweep restic das %02d:%02d falhou: %s", c.Hour, c.Minute, err.Error()),
						}); nerr != nil {
							logger.Error("worker: failed to record restic-failed notification", "cause", nerr.Error())
						}
					} else {
						if _, nerr := notificationsService.Create(tctx, notifications.CreateNotificationRequest{
							Type:    notifications.NotificationTypeBackupCompleted,
							Title:   "Backup geral (restic) concluído",
							Message: "Sweep de /DATA enviado ao Google Drive via restic.",
						}); nerr != nil {
							logger.Error("worker: failed to record restic-completed notification", "cause", nerr.Error())
						}
						logger.Info("worker: restic sweep completed", "tenant", t.Slug, "config_id", c.ID)
					}
				}
			}
		}
	}

	// Initial run so catch-up happens on boot, then every minute.
	for _, t := range listTenants(ctx) {
		startPickerFor(t)
	}
	runRollover()
	runBackup()
	runCurator()
	runOpenFinance()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	logger.Info("worker: started", "tick", "1m")
	for {
		select {
		case <-ctx.Done():
			logger.Info("worker: shutting down")
			return
		case <-ticker.C:
			for _, t := range listTenants(ctx) {
				startPickerFor(t)
			}
			runRollover()
			runBackup()
			runCurator()
			runOpenFinance()
		}
	}
}

// recordBackupRun stamps last_automatic_backup_at so the schedule UI can show the last successful run.
func recordBackupRun(ctx context.Context, pool *pgxpool.Pool, configID string) error {
	return db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE backup_configuration SET last_automatic_backup_at = now() WHERE id = $1`, configID)
		return err
	})
}

// runPgDump shells out to pg_dump (present in the runtime image alongside the binary — see
// Dockerfile) using -F c (pg_restore's custom format, already gzip-compressed, so no separate
// compression step is needed) straight against databaseURL. When recipient is non-nil, pg_dump's
// output is streamed through age's encryption (filippo.io/age — same convention as the rest of
// this codebase of hand-rolling rather than reaching for a large SDK, age's own README recommends
// exactly this as its Go library) straight to disk, so the plaintext dump never touches disk even
// transiently, and the resulting file is named "*.dump.age" instead of "*.dump" — nothing on this
// server can decrypt it; only whoever holds the matching age private key can (deliberately never
// stored here — see config.Config.BackupAgeRecipient). Returns the written file's base name and
// size on success; the partial file is removed on failure so a crashed dump never lingers looking
// like a valid backup.
func runPgDump(ctx context.Context, databaseURL, backupsDir string, recipient age.Recipient) (filename string, sizeBytes int64, err error) {
	if err := os.MkdirAll(backupsDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("creating backups dir: %w", err)
	}

	dbName := "atlas"
	if u, parseErr := url.Parse(databaseURL); parseErr == nil {
		if name := strings.TrimPrefix(u.Path, "/"); name != "" {
			dbName = name
		}
	}

	ext := ".dump"
	if recipient != nil {
		ext = ".dump.age"
	}
	filename = fmt.Sprintf("%s_%s%s", dbName, time.Now().Format("20060102_150405"), ext)
	fullPath := filepath.Join(backupsDir, filename)

	out, createErr := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if createErr != nil {
		return "", 0, fmt.Errorf("creating dump file: %w", createErr)
	}
	defer out.Close() //nolint:errcheck // best-effort; the explicit Close below is what's checked

	var dst io.WriteCloser = out
	if recipient != nil {
		encWriter, encErr := age.Encrypt(out, recipient)
		if encErr != nil {
			os.Remove(fullPath)
			return "", 0, fmt.Errorf("setting up age encryption: %w", encErr)
		}
		dst = encWriter
	}

	cmd := exec.CommandContext(ctx, "pg_dump", databaseURL, "-F", "c")
	cmd.Stdout = dst
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// age.Encrypt's writer must be Close()d to flush its final MAC/frame — closing it before
	// checking runErr would still leave a corrupt-but-present file on a pg_dump failure, hence the
	// os.Remove below regardless of which step failed.
	var closeErr error
	if recipient != nil {
		closeErr = dst.Close()
	}
	if statCloseErr := out.Close(); closeErr == nil {
		closeErr = statCloseErr
	}

	if runErr != nil {
		os.Remove(fullPath)
		return "", 0, fmt.Errorf("pg_dump: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	if closeErr != nil {
		os.Remove(fullPath)
		return "", 0, fmt.Errorf("finalizing dump file: %w", closeErr)
	}

	info, statErr := os.Stat(fullPath)
	if statErr != nil {
		return "", 0, fmt.Errorf("stat dump file: %w", statErr)
	}
	return filename, info.Size(), nil
}

// runResticSweep shells out to restic (present in the runtime image — see Dockerfile) to back up
// dataDir (a read-only bind mount of the host's /DATA) to repository, then prunes to the last 3
// daily snapshots. Replaces the old backup-homelab.sh systemd-timer bash script — same tool, same
// excludes, same retention, just driven by this process instead of host cron, so there's one
// backup surface to monitor instead of three (see the DR audit this consolidation was built from).
// Also replaces the separate Vaultwarden cron script — /DATA/AppData/vaultwarden/data was never
// excluded from the old sweep, so it was already covered here; nothing new needed for it.
func runResticSweep(ctx context.Context, repository, passwordFile, dataDir string) error {
	env := append(os.Environ(),
		"RESTIC_REPOSITORY="+repository,
		"RESTIC_PASSWORD_FILE="+passwordFile,
	)

	args := append([]string{"backup", dataDir, "--tag", "daily"}, resticExcludeArgs(dataDir)...)
	backupCmd := exec.CommandContext(ctx, "restic", args...)
	backupCmd.Env = env
	var stderr bytes.Buffer
	backupCmd.Stderr = &stderr
	if err := backupCmd.Run(); err != nil {
		return fmt.Errorf("restic backup: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	forgetCmd := exec.CommandContext(ctx, "restic", "forget", "--keep-daily", "3", "--prune")
	forgetCmd.Env = env
	stderr.Reset()
	forgetCmd.Stderr = &stderr
	if err := forgetCmd.Run(); err != nil {
		return fmt.Errorf("restic forget: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// resticExcludeArgs mirrors backup-homelab.sh's exclude list, rooted at dataDir (the mount point
// actually used inside this container) instead of the host's bare /DATA path. Immich is excluded
// on purpose — its library is backed up separately via Apple iCloud, not this repository.
func resticExcludeArgs(dataDir string) []string {
	patterns := []string{
		dataDir + "/AppData/immich",
		dataDir + "/AppData/immich-pet-tagger",
		dataDir + "/AppData/backup-logs",
		dataDir + "/AppData/openlist/data/temp",
		dataDir + "/AppData/*/data.db-shm",
		dataDir + "/AppData/*/data.db-wal",
	}
	args := make([]string, 0, len(patterns)*2)
	for _, p := range patterns {
		args = append(args, "--exclude", p)
	}
	return args
}

// pruneOldBackups deletes everything past the newest `keep` dumps. Filenames are
// "<db>_YYYYMMDD_HHMMSS.dump" or "..._....dump.age" — lexicographic order is chronological order,
// so a plain string sort is enough, no need to parse timestamps back out. Matches both suffixes so
// pruning still works across the rollout of age encryption (old plaintext dumps age out normally
// instead of being left behind forever once new backups start being named *.dump.age).
func pruneOldBackups(backupsDir string, keep int, logger *slog.Logger) {
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		logger.Error("worker: listing backups dir for pruning failed", "cause", err.Error())
		return
	}
	var dumps []string
	for _, e := range entries {
		if !e.IsDir() && (strings.HasSuffix(e.Name(), ".dump") || strings.HasSuffix(e.Name(), ".dump.age")) {
			dumps = append(dumps, e.Name())
		}
	}
	if len(dumps) <= keep {
		return
	}
	sort.Strings(dumps)
	for _, name := range dumps[:len(dumps)-keep] {
		if err := os.Remove(filepath.Join(backupsDir, name)); err != nil {
			logger.Error("worker: pruning old backup failed", "file", name, "cause", err.Error())
		}
	}
}

// humanizeBytes renders a byte count as a short human-readable size for the notification message
// (e.g. "12.3 MB") — dumps are always well above 1 KB in practice, so bytes/KB granularity isn't
// worth a branch.
func humanizeBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	units := "KMGT"
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), units[exp])
}

// pluggySyncInterval reads PLUGGY_SYNC_INTERVAL (a Go duration, e.g. "3h"); anything invalid or
// under 30 minutes falls back to the default — Pluggy refreshes banks on its own schedule, so
// polling faster than that only re-reads identical data and risks rate limits.
func pluggySyncInterval(logger *slog.Logger) time.Duration {
	raw := strings.TrimSpace(os.Getenv("PLUGGY_SYNC_INTERVAL"))
	if raw == "" {
		return openfinance.DefaultInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 30*time.Minute {
		logger.Warn("worker: invalid PLUGGY_SYNC_INTERVAL, using default", "value", raw, "default", openfinance.DefaultInterval.String())
		return openfinance.DefaultInterval
	}
	return d
}
