// Package config loads the process's environment configuration. DatabaseURL and
// TestDatabaseURL are deliberately separate fields from separate env vars — never derive one from
// the other — mirroring the existing DB_URL/TEST_DB_URL split
// (docs/architecture-plan.md section 8) that exists specifically to make "a test run touched the
// real database" structurally impossible.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port string
	// PprofAddr enables the private profiling listener when non-empty. Keep it bound to
	// loopback (127.0.0.1:6060) or an isolated diagnostics network; it is not exposed by the API mux.
	PprofAddr   string
	DatabaseURL string
	// DBStatementTimeout bounds PostgreSQL statement execution per connection. It is separate
	// from HTTP timeouts so slow dependencies cannot hold pool connections indefinitely.
	DBStatementTimeout time.Duration

	// GoogleClientID/GoogleClientSecret are optional — "Sign in with Google" is only registered
	// (internal/auth.RegisterPublicRoutes) when both are set to real values. FrontendOrigin
	// mirrors the old Java APP_FRONTEND_ORIGIN var: the redirect_uri Google sends the browser back
	// to must exactly match what's already authorized in Google Cloud Console for this client id.
	GoogleClientID     string
	GoogleClientSecret string
	FrontendOrigin     string

	// UploadsDir is where documents.Service stores locally-uploaded files — mirrors the old Java
	// LocalFileStorageService's app.uploads.dir (default ./uploads), one subfolder per Origin
	// (Faturas/Receitas/Exames/...). Files already migrated from production with a "googledrive:"
	// FilePath are unaffected — those are fetched from Drive, never this directory.
	UploadsDir string

	// BackupsDir is where cmd/worker writes gzipped pg_dump snapshots for the backup_configuration
	// schedule — a sibling of UploadsDir, same reasoning (bind-mounted separately in production so
	// dumps survive a container recreate/image update, not baked into the image).
	BackupsDir string

	// StaticDir is where the Next.js frontend's static export (NEXT_OUTPUT_EXPORT=1) lives, if the
	// Dockerfile's frontend-build stage produced one — see internal/spa. Defaults to a path that
	// doesn't exist in local dev (no frontend export there), which is fine: spa.NewHandler 404s
	// instead of panicking when the directory is missing.
	StaticDir string

	// BackupAgeRecipient is an age X25519 public key (age1...) — when set, cmd/worker encrypts every
	// pg_dump backup to it before writing to disk, so a stolen/leaked backup file is useless without
	// the matching private key (deliberately never stored on this server — see cmd/worker/main.go's
	// runPgDump doc comment). Empty means "not configured yet": backups stay in the old plain -F c
	// format rather than cmd/worker refusing to run, so the backup schedule doesn't silently stop
	// working during the rollout of this feature.
	BackupAgeRecipient string

	// OmniRouteURL/OmniRouteAPIKey/OmniRouteModel override the Postgres-backed pandora_config row
	// when set: internal/pandora.Service uses these directly instead of querying the DB, so the
	// OmniRoute migration doesn't require touching the old Pandora config at all. Empty
	// OmniRouteURL means "not overridden" — falls back to pandora_config as before.
	OmniRouteURL    string
	OmniRouteAPIKey string
	OmniRouteModel  string

	// OmniRouteExtractionModel pins document extraction (invoices, paystubs, lab results) to one
	// named model instead of letting OMNIROUTE_MODEL's combo route it. OMNIROUTE_MODEL is
	// typically a combo spreading load across providers including free tiers, which is fine for
	// chat but not for structured extraction: see pandora.Service.ExtractionModel for the
	// production incident that motivated this. Empty means "use the default routing".
	OmniRouteExtractionModel string

	// ResticRepository/ResticPasswordFile/ResticDataDir drive cmd/worker's host-wide restic sweep
	// (consolidates what used to be a separate systemd-timer bash script — see
	// docs/decisions or the DR audit this was built from). Empty ResticRepository means "not
	// configured" — the sweep step is skipped entirely, same degrade-gracefully idiom as
	// BackupAgeRecipient above, so an environment without restic set up doesn't break the existing
	// pg_dump schedule.
	ResticRepository   string
	ResticPasswordFile string
	ResticDataDir      string

	// DashboardChatToken gates POST /api/public/dashboard-chat — the one route served outside
	// AuthMiddleware. Empty means the route is NOT registered (fail closed); the dashboard
	// widget must send it as "Authorization: Bearer <token>" (or "X-Dashboard-Token: <token>").
	// DashboardChatAllowedOrigin is a comma-separated list of exact browser Origins the route
	// answers CORS for (the widget reaches the same page from the LAN IP, the Tailscale
	// name, etc. — each a distinct Origin). Empty => no CORS headers at all.
	DashboardChatToken         string
	DashboardChatAllowedOrigin string

	// FeatureFlagControlPlaneURL points internal/featureflags.Client at the feature-flag-mvp
	// service's Control Plane (github.com/richardnogueira01/feature-flag-mvp) — a separate
	// product, not Atlas-exclusive; Atlas is just its first real consumer. Empty means "not
	// configured": the /api/feature-flags/** routes stay registered but answer 404, so an Atlas
	// deployment without the feature-flag service running is unaffected.
	FeatureFlagControlPlaneURL string

	// AppEncryptionKey is the raw base64 APP_ENCRYPTION_KEY (same value validateEncryptionKey
	// checks). internal/platform/secretbox derives from it the key that encrypts each user's TOTP
	// secret at rest. Empty means MFA enrollment is refused rather than stored in the clear.
	AppEncryptionKey string

	// TurnstileSecretKey enables server-side captcha verification (Cloudflare Turnstile) on the
	// sensitive account endpoints when set. Empty disables the check entirely.
	TurnstileSecretKey string

	// MFATOTPIssuer is the label authenticator apps show next to the account. Defaults to "Atlas".
	MFATOTPIssuer string

	// AuditRetention is how long audit_events rows are kept before the background recorder prunes
	// them (AUDIT_RETENTION_DAYS, default 3 days). <= 0 disables pruning — the table then grows
	// without bound, which is only appropriate in tests.
	AuditRetention time.Duration

	// Tenant self-service (docs/tenant-self-service.md). TenantDefaultSlug/TenantDefaultSchema name
	// the tenant that owns the existing `public` data (the owner): schema is "public" until the
	// owner's domain tables are moved to tenant_<slug> in the cutover. SignupEnabled gates
	// POST /api/auth/signup; SignupInviteToken, when set, must be echoed in the signup body.
	TenantDefaultSlug   string
	TenantDefaultSchema string
	SignupEnabled       bool
	SignupInviteToken   string
	// TenantMasterKey is the envelope-encryption KEK (base64, 32 bytes) that wraps each tenant's
	// DEK. It must live outside the database. Empty means "not configured" — tenant provisioning
	// then fails loudly rather than storing data with no key hierarchy.
	TenantMasterKey string
}

// boolEnv parses a boolean env var; empty means false, an unparsable value is a boot error rather
// than a silent default.
func boolEnv(name string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("config: %s must be a boolean: %q", name, raw)
	}
	return v, nil
}

// placeholderSecrets are the values .env.example / docker-compose ship; a real deployment that
// forgot to fill one in must fail loudly at boot, not silently run with a known secret.
var placeholderSecrets = map[string]bool{"change-me": true, "changeme": true}

// validateEncryptionKey fails fast when APP_ENCRYPTION_KEY is set but unusable — a placeholder, or
// not base64 of a 16/24/32-byte AES key. Empty is still allowed: it means "no external
// integrations configured yet", and integrations.loadKey() already errors clearly at connect
// time. Without this, a mis-set key only surfaces the first time someone connects an integration.
func validateEncryptionKey(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if placeholderSecrets[raw] {
		return fmt.Errorf("config: APP_ENCRYPTION_KEY is still the placeholder %q — generate a real key (openssl rand -base64 32)", raw)
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("config: APP_ENCRYPTION_KEY is not valid base64: %w", err)
	}
	switch len(key) {
	case 16, 24, 32:
		return nil
	default:
		return fmt.Errorf("config: APP_ENCRYPTION_KEY must decode to 16, 24 or 32 bytes (AES-128/192/256), got %d", len(key))
	}
}

// validateMasterKey fails fast when TENANT_MASTER_KEY is set but unusable. Empty is allowed at
// config-load time (a deployment that hasn't adopted tenant encryption yet); the tenancy layer
// errors clearly the moment a key is actually needed.
func validateMasterKey(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if placeholderSecrets[raw] {
		return fmt.Errorf("config: TENANT_MASTER_KEY is still the placeholder %q — generate a real key (openssl rand -base64 32)", raw)
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("config: TENANT_MASTER_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return fmt.Errorf("config: TENANT_MASTER_KEY must decode to 32 bytes (AES-256), got %d", len(key))
	}
	return nil
}

// Load reads the process environment. DATABASE_URL is required; PORT defaults to 8080;
// FrontendOrigin defaults to http://localhost:3000 (the frontend's own local-dev default, and the
// origin the local-testing Google OAuth client is already registered against).
func Load() (Config, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return Config{}, fmt.Errorf("config: DATABASE_URL is required")
	}
	appEncryptionKey := os.Getenv("APP_ENCRYPTION_KEY")
	if err := validateEncryptionKey(appEncryptionKey); err != nil {
		return Config{}, err
	}
	if err := validateMasterKey(os.Getenv("TENANT_MASTER_KEY")); err != nil {
		return Config{}, err
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	frontendOrigin := os.Getenv("APP_FRONTEND_ORIGIN")
	if frontendOrigin == "" {
		frontendOrigin = "http://localhost:3000"
	}
	uploadsDir := os.Getenv("APP_UPLOADS_DIR")
	if uploadsDir == "" {
		uploadsDir = "./uploads"
	}
	backupsDir := os.Getenv("APP_BACKUPS_DIR")
	if backupsDir == "" {
		backupsDir = "./backups"
	}
	staticDir := os.Getenv("APP_STATIC_DIR")
	if staticDir == "" {
		staticDir = "./frontend"
	}
	dbStatementTimeout := 5 * time.Second
	if rawTimeout := strings.TrimSpace(os.Getenv("DB_STATEMENT_TIMEOUT")); rawTimeout != "" {
		parsedTimeout, err := time.ParseDuration(rawTimeout)
		if err != nil || parsedTimeout <= 0 {
			return Config{}, fmt.Errorf("config: DB_STATEMENT_TIMEOUT must be a positive duration: %q", rawTimeout)
		}
		dbStatementTimeout = parsedTimeout
	}
	resticDataDir := os.Getenv("RESTIC_DATA_DIR")
	if resticDataDir == "" {
		resticDataDir = "/app/DATA"
	}
	tenantDefaultSlug := os.Getenv("TENANT_DEFAULT_SLUG")
	if tenantDefaultSlug == "" {
		tenantDefaultSlug = "owner"
	}
	tenantDefaultSchema := os.Getenv("TENANT_DEFAULT_SCHEMA")
	if tenantDefaultSchema == "" {
		tenantDefaultSchema = "public"
	}
	signupEnabled, err := boolEnv("SIGNUP_ENABLED")
	if err != nil {
		return Config{}, err
	}
	mfaIssuer := strings.TrimSpace(os.Getenv("MFA_TOTP_ISSUER"))
	if mfaIssuer == "" {
		mfaIssuer = "Atlas"
	}
	auditRetentionDays := 3
	if raw := strings.TrimSpace(os.Getenv("AUDIT_RETENTION_DAYS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return Config{}, fmt.Errorf("config: AUDIT_RETENTION_DAYS must be a non-negative integer: %q", raw)
		}
		auditRetentionDays = parsed
	}
	return Config{
		Port:                     port,
		PprofAddr:                os.Getenv("PPROF_ADDR"),
		DatabaseURL:              dsn,
		DBStatementTimeout:       dbStatementTimeout,
		GoogleClientID:           os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:       os.Getenv("GOOGLE_CLIENT_SECRET"),
		FrontendOrigin:           frontendOrigin,
		UploadsDir:               uploadsDir,
		BackupsDir:               backupsDir,
		StaticDir:                staticDir,
		BackupAgeRecipient:       os.Getenv("APP_BACKUP_AGE_RECIPIENT"),
		OmniRouteURL:             os.Getenv("OMNIROUTE_URL"),
		OmniRouteAPIKey:          os.Getenv("OMNIROUTE_API_KEY"),
		OmniRouteModel:           os.Getenv("OMNIROUTE_MODEL"),
		OmniRouteExtractionModel: os.Getenv("OMNIROUTE_EXTRACTION_MODEL"),
		ResticRepository:         os.Getenv("RESTIC_REPOSITORY"),
		ResticPasswordFile:       os.Getenv("RESTIC_PASSWORD_FILE"),
		ResticDataDir:            resticDataDir,

		DashboardChatToken:         os.Getenv("DASHBOARD_CHAT_TOKEN"),
		DashboardChatAllowedOrigin: os.Getenv("DASHBOARD_CHAT_ALLOWED_ORIGIN"),

		FeatureFlagControlPlaneURL: os.Getenv("FEATURE_FLAG_CONTROL_PLANE_URL"),

		AppEncryptionKey:   appEncryptionKey,
		TurnstileSecretKey: strings.TrimSpace(os.Getenv("TURNSTILE_SECRET_KEY")),
		MFATOTPIssuer:      mfaIssuer,
		AuditRetention:     time.Duration(auditRetentionDays) * 24 * time.Hour,

		TenantDefaultSlug:   tenantDefaultSlug,
		TenantDefaultSchema: tenantDefaultSchema,
		SignupEnabled:       signupEnabled,
		SignupInviteToken:   os.Getenv("SIGNUP_INVITE_TOKEN"),
		TenantMasterKey:     os.Getenv("TENANT_MASTER_KEY"),
	}, nil
}
