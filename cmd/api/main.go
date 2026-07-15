package main

//go:generate bun run --cwd ../../web build

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	authmiddlewares "github.com/gsoultan/panmail/internal/auth/middlewares"
	authstores "github.com/gsoultan/panmail/internal/auth/repositories/stores/postgres"
	authservices "github.com/gsoultan/panmail/internal/auth/services"
	authusecases "github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/internal/config"
	emailstores "github.com/gsoultan/panmail/internal/email/repositories/stores/postgres"
	emailservices "github.com/gsoultan/panmail/internal/email/services"
	emailconnect "github.com/gsoultan/panmail/internal/email/transports/connect"
	emailusecases "github.com/gsoultan/panmail/internal/email/usecases"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/stores/postgres"
	providerservices "github.com/gsoultan/panmail/internal/email_provider/services"
	providerconnect "github.com/gsoultan/panmail/internal/email_provider/transports/connect"
	providerusecases "github.com/gsoultan/panmail/internal/email_provider/usecases"
	eventstores "github.com/gsoultan/panmail/internal/event/repositories/stores/pebble"
	eventservices "github.com/gsoultan/panmail/internal/event/services"
	eventhttp "github.com/gsoultan/panmail/internal/event/transports/http"
	eventusecases "github.com/gsoultan/panmail/internal/event/usecases"
	inboundstores "github.com/gsoultan/panmail/internal/inbound/repositories/stores/pebble"
	inboundservices "github.com/gsoultan/panmail/internal/inbound/services"
	inboundhttp "github.com/gsoultan/panmail/internal/inbound/transports/http"
	inboundusecases "github.com/gsoultan/panmail/internal/inbound/usecases"
	inboundworker "github.com/gsoultan/panmail/internal/inbound/worker"
	"github.com/gsoultan/panmail/internal/logging"
	setupservices "github.com/gsoultan/panmail/internal/setup/services"
	setupusecases "github.com/gsoultan/panmail/internal/setup/usecases"
	suppressionstores "github.com/gsoultan/panmail/internal/suppression/repositories/stores/postgres"
	suppressionservices "github.com/gsoultan/panmail/internal/suppression/services"
	suppressionusecases "github.com/gsoultan/panmail/internal/suppression/usecases"
	settingsservices "github.com/gsoultan/panmail/internal/system_settings/services"
	settingsusecases "github.com/gsoultan/panmail/internal/system_settings/usecases"
	templatestores "github.com/gsoultan/panmail/internal/template/repositories/stores/postgres"
	templateservices "github.com/gsoultan/panmail/internal/template/services"
	templateusecases "github.com/gsoultan/panmail/internal/template/usecases"
	tenantstores "github.com/gsoultan/panmail/internal/tenant/repositories/stores/postgres"
	tenantservices "github.com/gsoultan/panmail/internal/tenant/services"
	tenantusecases "github.com/gsoultan/panmail/internal/tenant/usecases"
	webhookstores "github.com/gsoultan/panmail/internal/webhook/repositories/stores/postgres"
	webhookservices "github.com/gsoultan/panmail/internal/webhook/services"
	webhookusecases "github.com/gsoultan/panmail/internal/webhook/usecases"
	webhookworker "github.com/gsoultan/panmail/internal/webhook/worker"
	"github.com/gsoultan/panmail/pkg/auth"
	"github.com/gsoultan/panmail/pkg/db"
	"github.com/gsoultan/panmail/web"
	_ "github.com/lib/pq"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	_ "modernc.org/sqlite"
)

const (
	defaultPort = "8080"
	dbConnStr   = "host=localhost port=5432 user=panmail password=panmail dbname=panmail sslmode=disable"
)

func (m *multiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		newHandlers[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: newHandlers}
}

type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, record.Level) {
			_ = h.Handle(ctx, record)
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		newHandlers[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: newHandlers}
}

var (
	Version = "development"
)

func main() {
	// Check for build command before parsing other flags
	if len(os.Args) > 1 && os.Args[1] == "build" {
		handleBuildCommand()
		return
	}

	// Parse flags for server mode
	builtUIFlag := flag.Bool("built-ui", false, "Serve the built UI (embedded or from web/dist)")
	configFlag := flag.String("config", "", "Path to configuration file")
	logDirFlag := flag.String("log-dir", "logs.db", "Directory for logs database")
	eventDirFlag := flag.String("event-dir", "events.db", "Directory for events database")
	inboundDirFlag := flag.String("inbound-dir", "inbound.db", "Directory for inbound database")
	versionFlag := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("Panmail Gateway v%s\n", Version)
		return
	}

	if *configFlag != "" {
		config.SetConfigPath(*configFlag)
	}

	// Ensure UI is built if --built-ui is passed and we're in dev mode (not embedded)
	if *builtUIFlag && !web.IsBuiltUI {
		slog.Info("Building UI to ensure it's updated...")
		buildUI("development")
	}

	// 1. Setup Logging (Pebble + Stdout)
	logStore, err := logging.NewPebbleStore(*logDirFlag)
	if err != nil {
		fmt.Printf("failed to open log store: %v\n", err)
		os.Exit(1)
	}
	defer logStore.Close()

	pebbleHandler := logging.NewPebbleHandler(logStore, slog.LevelInfo)
	stdoutHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})

	// Multi-handler
	handler := &multiHandler{handlers: []slog.Handler{stdoutHandler, pebbleHandler}}
	slog.SetDefault(slog.New(handler))

	logService := logging.NewService(logStore)

	// 2. Setup DB Connection Manager
	conn := db.NewConnection(nil)

	// Try to load config from ~/.panmail/db_config.yaml
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
	}

	if cfg != nil {
		sqlDB, err := db.Connect(cfg.Database)
		if err != nil {
			slog.Error("failed to connect to db from config", "error", err)
		} else {
			conn.SetDB(sqlDB)
			// Run migration if connected
			if err := migrate(conn, cfg.Database.Type); err != nil {
				slog.Error("failed to migrate db", "error", err)
			}
		}
	} else {
		slog.Info("First run detected: no configuration found. Waiting for setup.")
	}

	// 3. Initialize Auth
	swappableTokenMaker := auth.NewSwappableTokenMaker(nil)
	if cfg != nil && cfg.Auth.SymmetricKey != "" {
		maker, err := auth.NewPasetoMaker(cfg.Auth.SymmetricKey)
		if err != nil {
			slog.Error("failed to initialize paseto maker", "error", err)
		} else {
			swappableTokenMaker.SetMaker(maker)
		}
	}

	// 4. Setup Layered Architecture
	providerRepo := postgres.NewStore(conn)
	userRepo := authstores.NewStore(conn)
	tenantRepo := tenantstores.NewStore(conn)
	apiKeyRepo := authstores.NewApiKeyStore(conn)
	templateRepo := templatestores.NewStore(conn)
	suppressionRepo := suppressionstores.NewStore(conn)
	outboxRepo := emailstores.NewOutboxStore(conn)
	webhookRepo := webhookstores.NewStore(conn)
	eventRepo, err := eventstores.NewStore(*eventDirFlag)
	if err != nil {
		slog.Error("failed to open event store", "error", err)
		os.Exit(1)
	}
	defer eventRepo.Close()

	inboundRepo, err := inboundstores.NewStore(*inboundDirFlag)
	if err != nil {
		slog.Error("failed to open inbound store", "error", err)
		os.Exit(1)
	}
	defer inboundRepo.Close()

	providerFactory := providerusecases.NewProviderFactory()

	manageProvidersUsecase := providerusecases.NewManageProvidersUsecase(providerRepo, providerFactory)
	emailProviderService := providerservices.NewEmailProviderService(manageProvidersUsecase)

	manageTemplatesUsecase := templateusecases.NewManageTemplatesUsecase(templateRepo)
	templateService := templateservices.NewTemplateService(manageTemplatesUsecase)

	manageSuppressionsUsecase := suppressionusecases.NewManageSuppressionsUsecase(suppressionRepo)
	suppressionService := suppressionservices.NewSuppressionService(manageSuppressionsUsecase)

	webhookUsecase := webhookusecases.NewWebhookUsecase(webhookRepo)
	webhookService := webhookservices.NewWebhookService(webhookUsecase)

	settingsUsecase := settingsusecases.NewSettingsUsecase()
	settingsService := settingsservices.NewSettingsService(settingsUsecase)

	outboundWebhookWorker := webhookworker.NewWebhookWorker(webhookUsecase)
	go outboundWebhookWorker.Start(context.Background())

	processEventUsecase := eventusecases.NewProcessEventUsecase(eventRepo, inboundRepo, outboundWebhookWorker)
	eventService := eventservices.NewEventService(processEventUsecase)
	webhookHandler := eventhttp.NewWebhookHandler(processEventUsecase, providerRepo)

	// Start background tasks
	retentionDays := 14
	if cfg != nil && cfg.App.LogRetentionDays > 0 {
		retentionDays = cfg.App.LogRetentionDays
	}
	go processEventUsecase.StartCleanupTask(context.Background(), 24*time.Hour, retentionDays)

	inboundUsecase := inboundusecases.NewInboundUsecase(inboundRepo, processEventUsecase, outboundWebhookWorker)
	inboundService := inboundservices.NewInboundService(inboundUsecase)
	inboundWebhookHandler := inboundhttp.NewWebhookHandler(inboundUsecase)

	baseURL := ""
	if cfg != nil {
		baseURL = cfg.App.BaseURL
	}
	tenantUsecase := tenantusecases.NewTenantUsecase(tenantRepo)
	templateRenderer := emailusecases.NewTemplateRenderer()
	sendEmailUsecase := emailusecases.NewSendEmailUsecase(providerRepo, templateRepo, suppressionRepo, outboxRepo, processEventUsecase, providerFactory, templateRenderer, baseURL)
	emailService := emailservices.NewEmailService(sendEmailUsecase)

	queueWorker := emailusecases.NewQueueWorker(outboxRepo, sendEmailUsecase, manageSuppressionsUsecase, tenantUsecase, 5*time.Second)
	go queueWorker.Start(context.Background())

	trackingHandler := eventhttp.NewTrackingHandler(processEventUsecase)

	poller := inboundworker.NewPoller(tenantRepo, providerRepo, inboundUsecase, providerFactory, 30*time.Second)
	go poller.Start(context.Background()) // In production, use a separate context for shutdown

	authUsecase := authusecases.NewAuthUsecase(userRepo, tenantRepo, swappableTokenMaker)
	authService := authservices.NewAuthService(authUsecase)

	userUsecase := authusecases.NewUserUsecase(userRepo)
	userService := authservices.NewUserService(userUsecase)

	apiKeyUsecase := authusecases.NewApiKeyUsecase(apiKeyRepo)
	apiKeyService := authservices.NewApiKeyService(apiKeyUsecase)

	tenantService := tenantservices.NewTenantService(tenantUsecase)

	authMiddleware := authmiddlewares.NewAuthMiddleware(swappableTokenMaker, apiKeyUsecase)

	setupUsecase := setupusecases.NewSetupUsecase(authUsecase, conn, swappableTokenMaker, migrate)
	setupService := setupservices.NewSetupService(setupUsecase)

	// 5. Setup Health Check
	healthChecker := grpchealth.NewStaticChecker()

	// 6. Setup ConnectRPC Transport
	rbacInterceptor := authmiddlewares.NewRBACInterceptor()
	interceptors := connect.WithInterceptors(rbacInterceptor)

	mux := http.NewServeMux()
	mux.Handle(providerconnect.NewHandler(emailProviderService, interceptors))
	mux.Handle(emailconnect.NewHandler(emailService, interceptors))
	mux.Handle(panmailv1connect.NewAuthServiceHandler(authService, interceptors))
	mux.Handle(panmailv1connect.NewApiKeyServiceHandler(apiKeyService, interceptors))
	mux.Handle(panmailv1connect.NewUserServiceHandler(userService, interceptors))
	mux.Handle(panmailv1connect.NewTenantServiceHandler(tenantService, interceptors))
	mux.Handle(panmailv1connect.NewSetupServiceHandler(setupService)) // Setup usually doesn't need auth/rbac
	mux.Handle(panmailv1connect.NewTemplateServiceHandler(templateService, interceptors))
	mux.Handle(panmailv1connect.NewSuppressionServiceHandler(suppressionService, interceptors))
	mux.Handle(panmailv1connect.NewWebhookServiceHandler(webhookService, interceptors))
	mux.Handle(panmailv1connect.NewSystemSettingsServiceHandler(settingsService, interceptors))
	mux.Handle(panmailv1connect.NewEventServiceHandler(eventService, interceptors))
	mux.Handle(panmailv1connect.NewInboundServiceHandler(inboundService, interceptors))
	mux.Handle(panmailv1connect.NewLogServiceHandler(logService, interceptors))
	mux.Handle(grpchealth.NewHandler(healthChecker))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		// Respond with 200 OK if the service is serving, otherwise 503
		// Using empty string for service name checks the overall server health
		res, err := healthChecker.Check(r.Context(), &grpchealth.CheckRequest{Service: ""})
		if err != nil || res.Status != grpchealth.StatusServing {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Service Unavailable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.Handle("/webhooks/", webhookHandler)
	mux.Handle("/inbound/", inboundWebhookHandler)
	mux.HandleFunc("/track/open/", trackingHandler.HandleOpen)
	mux.HandleFunc("/track/click/", trackingHandler.HandleClick)

	// 6. Serve Frontend (Embedded or Disk)
	serveUI := *builtUIFlag || web.IsBuiltUI
	if serveUI {
		var uiFS fs.FS
		if web.IsBuiltUI {
			slog.Info("serving embedded UI")
			var err error
			uiFS, err = fs.Sub(web.Dist, "dist")
			if err != nil {
				slog.Error("failed to create sub fs for dist", "error", err)
			}
		} else {
			slog.Info("serving UI from filesystem (web/dist)")
			uiFS = os.DirFS("web/dist")
		}

		if uiFS != nil {
			fileServer := http.FileServer(http.FS(uiFS))
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				// Check setup status
				isSetup, _ := setupUsecase.IsSetup(r.Context())

				// If it's the root path and not setup, redirect to /setup
				if r.URL.Path == "/" && !isSetup {
					slog.Info("redirecting to setup")
					http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
					return
				}

				// If it's the setup path and already setup, redirect to root
				if r.URL.Path == "/setup" && isSetup {
					slog.Info("already setup, redirecting to root")
					http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
					return
				}

				// Check if file exists in the UI FS
				f, err := uiFS.Open(strings.TrimPrefix(r.URL.Path, "/"))
				if err == nil {
					f.Close()
					fileServer.ServeHTTP(w, r)
					return
				}

				// Fallback to index.html for SPA
				indexHTML, err := fs.ReadFile(uiFS, "index.html")
				if err != nil {
					http.Error(w, "index.html not found", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write(indexHTML)
			})
		}
	} else {
		slog.Info("UI not embedded in this build and --built-ui flag not provided")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: h2c.NewHandler(authMiddleware.Handle(mux), &http2.Server{}),
	}

	// 7. Graceful Shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("Starting Email Gateway API", "port", port)
		healthChecker.SetStatus("", grpchealth.StatusServing)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("failed to start server", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	slog.Info("Shutting down server...")
	healthChecker.SetStatus("", grpchealth.StatusNotServing)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Server stopped gracefully")
}

func buildUI(version string) {
	fmt.Printf("🚀 Building UI (version: %s)...\n", version)

	// Check if bun is installed
	if _, err := exec.LookPath("bun"); err != nil {
		fmt.Println("❌ Error: 'bun' is not installed. Please install bun to build the UI.")
		os.Exit(1)
	}

	cmd := exec.Command("bun", "install")
	cmd.Dir = "web"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("❌ Failed to run bun install: %v\n", err)
		os.Exit(1)
	}

	cmd = exec.Command("bun", "run", "build")
	cmd.Dir = "web"
	cmd.Env = append(os.Environ(), fmt.Sprintf("VITE_APP_VERSION=%s", version))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("❌ Failed to build UI: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ UI built successfully.")
}

func handleBuildCommand() {
	buildFlags := flag.NewFlagSet("build", flag.ExitOnError)
	builtUI := buildFlags.Bool("built-ui", false, "Build and embed the UI")
	version := buildFlags.String("version", "development", "Version to build")

	_ = buildFlags.Parse(os.Args[2:])

	// Check if we are in the project root by checking for go.mod
	if _, err := os.Stat("go.mod"); os.IsNotExist(err) {
		fmt.Println("❌ Error: build command must be run from the project root.")
		os.Exit(1)
	}

	if *builtUI {
		buildUI(*version)
	}

	fmt.Println("🚀 Building Panmail backend...")

	// ldflags needs to be a single string for the -ldflags flag
	ldflags := fmt.Sprintf("-s -w -X main.Version=%s", *version)
	args := []string{"build"}
	if *builtUI {
		args = append(args, "-tags", "builtui")
	}
	args = append(args, "-ldflags", ldflags, "-o", "panmail", "./cmd/api")

	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("❌ Failed to build backend: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Panmail built successfully.")
}

func migrate(conn db.Connection, dbType string) error {
	db := conn.GetDB()
	if db == nil {
		return errors.New("database not connected")
	}
	jsonType := "JSONB"
	uuidType := "UUID"
	timestampType := "TIMESTAMP WITH TIME ZONE"

	if dbType == "sqlite" || dbType == "mysql" || dbType == "mariadb" {
		jsonType = "TEXT"
		uuidType = "VARCHAR(36)"
		timestampType = "DATETIME"
	}

	queries := []string{
		fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS tenants (
			id %s PRIMARY KEY,
			name TEXT NOT NULL,
			retry_pattern %s,
			created_at %s NOT NULL,
			updated_at %s NOT NULL
		);`, uuidType, jsonType, timestampType, timestampType),
		fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS users (
			id %s PRIMARY KEY,
			tenant_id %s NOT NULL,
			email VARCHAR(255) NOT NULL UNIQUE,
			password TEXT NOT NULL,
			name TEXT NOT NULL,
			role VARCHAR(50) NOT NULL DEFAULT 'USER_ROLE_VIEWER',
			two_factor_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			two_factor_secret TEXT,
			created_at %s NOT NULL,
			updated_at %s NOT NULL,
			FOREIGN KEY (tenant_id) REFERENCES tenants(id)
		);`, uuidType, uuidType, timestampType, timestampType),
		fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS email_providers (
			id %s PRIMARY KEY,
			tenant_id %s NOT NULL,
			name TEXT NOT NULL,
			type INTEGER NOT NULL,
			config %s NOT NULL,
			allowed_domains %s,
			created_at %s NOT NULL,
			updated_at %s NOT NULL,
			FOREIGN KEY (tenant_id) REFERENCES tenants(id),
			UNIQUE(tenant_id, name)
		);`, uuidType, uuidType, jsonType, jsonType, timestampType, timestampType),
		fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS api_keys (
			id %s PRIMARY KEY,
			tenant_id %s NOT NULL,
			name TEXT NOT NULL,
			key_hash TEXT NOT NULL,
			prefix VARCHAR(10) NOT NULL,
			last_used_at %s,
			expires_at %s,
			is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
			created_at %s NOT NULL,
			updated_at %s NOT NULL,
			FOREIGN KEY (tenant_id) REFERENCES tenants(id)
		);`, uuidType, uuidType, timestampType, timestampType, timestampType, timestampType),
		fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS templates (
			id %s PRIMARY KEY,
			tenant_id %s NOT NULL,
			name TEXT NOT NULL,
			subject TEXT NOT NULL,
			body_html TEXT NOT NULL,
			body_text TEXT NOT NULL,
			design TEXT,
			created_at %s NOT NULL,
			updated_at %s NOT NULL,
			FOREIGN KEY (tenant_id) REFERENCES tenants(id)
		);`, uuidType, uuidType, timestampType, timestampType),
		fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS suppressions (
			id %s PRIMARY KEY,
			tenant_id %s NOT NULL,
			email VARCHAR(255) NOT NULL,
			reason TEXT,
			created_at %s NOT NULL,
			FOREIGN KEY (tenant_id) REFERENCES tenants(id),
			UNIQUE(tenant_id, email)
		);`, uuidType, uuidType, timestampType),
		fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS webhooks (
			id %s PRIMARY KEY,
			tenant_id %s NOT NULL,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			events %s NOT NULL,
			active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at %s NOT NULL,
			updated_at %s NOT NULL,
			FOREIGN KEY (tenant_id) REFERENCES tenants(id)
		);`, uuidType, uuidType, jsonType, timestampType, timestampType),
		fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS outbox (
			id %s PRIMARY KEY,
			tenant_id %s NOT NULL,
			request %s NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_retry_at %s NOT NULL,
			last_error TEXT,
			created_at %s NOT NULL,
			updated_at %s NOT NULL,
			FOREIGN KEY (tenant_id) REFERENCES tenants(id)
		);`, uuidType, uuidType, jsonType, timestampType, timestampType, timestampType),
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			slog.Error("migration failed", "query", q, "error", err)
			return err
		}
	}

	// Handle adding columns to existing tables
	alterQueries := []string{
		fmt.Sprintf("ALTER TABLE tenants ADD COLUMN retry_pattern %s;", jsonType),
		fmt.Sprintf("ALTER TABLE users ADD COLUMN tenant_id %s;", uuidType),
		fmt.Sprintf("ALTER TABLE email_providers ADD COLUMN tenant_id %s;", uuidType),
		"ALTER TABLE users ADD COLUMN role VARCHAR(50) NOT NULL DEFAULT 'USER_ROLE_VIEWER';",
		fmt.Sprintf("ALTER TABLE api_keys ADD COLUMN expires_at %s;", timestampType),
		"ALTER TABLE api_keys ADD COLUMN is_enabled BOOLEAN NOT NULL DEFAULT TRUE;",
		fmt.Sprintf("ALTER TABLE email_providers ADD COLUMN allowed_domains %s;", jsonType),
		"ALTER TABLE templates ADD COLUMN design TEXT;",
		"ALTER TABLE users ADD COLUMN two_factor_enabled BOOLEAN NOT NULL DEFAULT FALSE;",
		"ALTER TABLE users ADD COLUMN two_factor_secret TEXT;",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_email_providers_tenant_name ON email_providers(tenant_id, name);",
		"CREATE INDEX IF NOT EXISTS idx_users_tenant_id ON users(tenant_id);",
		"CREATE INDEX IF NOT EXISTS idx_api_keys_tenant_id ON api_keys(tenant_id);",
		"CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash);",
		"CREATE INDEX IF NOT EXISTS idx_templates_tenant_id ON templates(tenant_id);",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_templates_tenant_name ON templates(tenant_id, name);",
		"CREATE INDEX IF NOT EXISTS idx_webhooks_tenant_id ON webhooks(tenant_id);",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_webhooks_tenant_name ON webhooks(tenant_id, name);",
		"CREATE INDEX IF NOT EXISTS idx_outbox_tenant_id ON outbox(tenant_id);",
		"CREATE INDEX IF NOT EXISTS idx_outbox_status_next_retry ON outbox(status, next_retry_at);",
		"CREATE INDEX IF NOT EXISTS idx_suppressions_tenant_id ON suppressions(tenant_id);",
		"CREATE INDEX IF NOT EXISTS idx_tenants_name ON tenants(name);",
	}

	for _, q := range alterQueries {
		_, _ = db.Exec(q) // Ignore error if column already exists
	}

	return nil
}
