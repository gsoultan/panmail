package main

//go:generate bun run --cwd ../../web build

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	authmiddlewares "github.com/gsoultan/panmail/internal/auth/middlewares"
	authstores "github.com/gsoultan/panmail/internal/auth/repositories/stores/postgres"
	authservices "github.com/gsoultan/panmail/internal/auth/services"
	authusecases "github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/internal/backup"
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
	"github.com/gsoultan/panmail/internal/health"
	inboundstores "github.com/gsoultan/panmail/internal/inbound/repositories/stores/pebble"
	inboundservices "github.com/gsoultan/panmail/internal/inbound/services"
	inboundhttp "github.com/gsoultan/panmail/internal/inbound/transports/http"
	inboundusecases "github.com/gsoultan/panmail/internal/inbound/usecases"
	inboundworker "github.com/gsoultan/panmail/internal/inbound/worker"
	"github.com/gsoultan/panmail/internal/logging"
	migrator "github.com/gsoultan/panmail/internal/migrate"
	"github.com/gsoultan/panmail/internal/observability"
	"github.com/gsoultan/panmail/internal/ratelimit"
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
	"github.com/gsoultan/panmail/pkg/secrets"
	"github.com/gsoultan/panmail/pkg/tracking"
	"github.com/gsoultan/panmail/web"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	_ "modernc.org/sqlite"
)

const (
	defaultPort = "8080"

	// Server limits. An email gateway is internet-facing, so a connection that
	// never finishes must not be able to hold a slot open forever.
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	writeTimeout      = 120 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 1 << 20 // 1 MiB

	// The largest RPC body accepted, before any handler runs.
	//
	// Sized for the biggest thing anyone legitimately sends: a message whose
	// attachments come to emailusecases.MaxAttachmentBytes, plus the base64
	// expansion of about a third that protojson applies to them, plus the body
	// and headers. Comfortably above a real email and far below what it takes
	// to exhaust a process.
	maxRPCRequestBytes = 48 << 20 // 48 MiB

	shutdownTimeout    = 15 * time.Second
	workerDrainTimeout = 30 * time.Second
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

	// An explicit operator action rather than something that happens on
	// startup. Rewriting every stored credential is not a thing a process
	// should do because it happened to restart with a new environment
	// variable — the operator needs to choose the moment, and to see the
	// result.
	if len(os.Args) > 1 && os.Args[1] == "rotate-secrets" {
		handleRotateSecretsCommand()
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "backup" {
		handleBackupCommand()
		return
	}

	// Parse flags for server mode
	builtUIFlag := flag.Bool("built-ui", false, "Serve the built UI (embedded or from web/dist)")
	configFlag := flag.String("config", "", "Path to configuration file")
	logDirFlag := flag.String("log-dir", "logs.db", "Directory for logs database")
	eventDirFlag := flag.String("event-dir", "events.db", "Directory for events database")
	inboundDirFlag := flag.String("inbound-dir", "inbound.db", "Directory for inbound database")
	versionFlag := flag.Bool("version", false, "Show version and exit")
	// Loopback by default. Queue depths and send volumes are operational
	// detail, and this gateway is internet-facing — serving them from the same
	// listener as the API would publish them to anyone who asked. A scraper
	// runs alongside, or an operator forwards the port deliberately.
	metricsAddrFlag := flag.String("metrics-addr", "127.0.0.1:9090",
		"Address for the Prometheus metrics listener; empty to disable")
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

	// 3b. Derive the data encryption key for stored credentials. It is kept
	// separate from the token signing key so the two can be rotated and
	// compromised independently.
	var keyring *secrets.Keyring
	if cfg != nil {
		dataKey, err := config.EnsureDataKey(cfg)
		if err != nil {
			slog.Error("failed to resolve the data encryption key", "error", err)
			os.Exit(1)
		}
		// Retired keys are decrypt-only, so values written before a rotation
		// stay readable while they are moved across. Without them a key can
		// never be replaced: reading what the old key wrote needs the old key.
		retired := secrets.ParseRetiredKeys(os.Getenv(secrets.EnvRetiredKeysName))
		keyring, err = secrets.NewKeyring(dataKey, retired...)
		if err != nil {
			slog.Error("failed to initialize the data encryption keyring", "error", err)
			os.Exit(1)
		}
		if len(retired) > 0 {
			slog.Info("secret key rotation in progress",
				"primary_key", keyring.PrimaryKeyID(), "retired_keys", len(retired))
		}
	}

	// Tracking links are signed so recorded opens and clicks can be trusted
	// and the click endpoint cannot be used as an open redirect.
	trackingSigner, err := newTrackingSigner(cfg)
	if err != nil {
		slog.Error("failed to initialize the tracking link signer", "error", err)
		os.Exit(1)
	}

	// 4. Setup Layered Architecture
	providerRepo := postgres.NewStore(conn, keyring)
	userRepo := authstores.NewStore(conn)
	tenantRepo := tenantstores.NewStore(conn)
	apiKeyRepo := authstores.NewApiKeyStore(conn)
	templateRepo := templatestores.NewStore(conn)
	suppressionRepo := suppressionstores.NewStore(conn)
	outboxRepo := emailstores.NewOutboxStore(conn)
	webhookRepo := webhookstores.NewStore(conn, keyring)
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
	domainHealthUsecase := providerusecases.NewDomainHealthUsecase(providerRepo)
	emailProviderService := providerservices.NewEmailProviderService(manageProvidersUsecase, domainHealthUsecase)

	manageTemplatesUsecase := templateusecases.NewManageTemplatesUsecase(templateRepo)
	templateService := templateservices.NewTemplateService(manageTemplatesUsecase)

	manageSuppressionsUsecase := suppressionusecases.NewManageSuppressionsUsecase(suppressionRepo)
	suppressionService := suppressionservices.NewSuppressionService(manageSuppressionsUsecase)

	webhookUsecase := webhookusecases.NewWebhookUsecase(webhookRepo)
	webhookService := webhookservices.NewWebhookService(webhookUsecase)

	settingsUsecase := settingsusecases.NewSettingsUsecase()
	settingsService := settingsservices.NewSettingsService(settingsUsecase)

	// Background work runs under a context this process controls, so shutdown
	// can stop the workers before the stores they write to are closed.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	var workers sync.WaitGroup

	// Persisted rather than buffered. The previous worker held an in-memory
	// channel and lost notifications three ways — a failing endpoint, a full
	// queue, a restart — which for the mechanism that tells a tenant their mail
	// bounced is silent, unrecoverable loss.
	webhookDeliveryRepo := webhookstores.NewDeliveryStore(conn)
	outboundWebhookWorker := webhookworker.NewDurableWorker(webhookDeliveryRepo, webhookUsecase)
	runWorker(&workers, "outbound-webhooks", func() { outboundWebhookWorker.Start(workerCtx) })

	processEventUsecase := eventusecases.NewProcessEventUsecase(eventRepo, inboundRepo, outboxRepo, providerRepo, outboundWebhookWorker)
	eventService := eventservices.NewEventService(processEventUsecase)
	webhookHandler := eventhttp.NewWebhookHandler(processEventUsecase, providerRepo)

	// Start background tasks
	retentionDays := 14
	if cfg != nil && cfg.App.LogRetentionDays > 0 {
		retentionDays = cfg.App.LogRetentionDays
	}
	runWorker(&workers, "log-retention", func() {
		processEventUsecase.StartCleanupTask(workerCtx, 24*time.Hour, retentionDays)
	})

	inboundUsecase := inboundusecases.NewInboundUsecase(inboundRepo, processEventUsecase, outboundWebhookWorker)
	inboundService := inboundservices.NewInboundService(inboundUsecase)
	inboundWebhookHandler := inboundhttp.NewWebhookHandler(inboundUsecase)

	baseURL := ""
	if cfg != nil {
		baseURL = cfg.App.BaseURL
	}
	tenantUsecase := tenantusecases.NewTenantUsecase(tenantRepo)
	templateRenderer := emailusecases.NewTemplateRenderer()
	// One limiter shared by the API path and the outbox worker, so a tenant
	// cannot get twice its allowance by using both. Its state is per process;
	// see the package comment for what that means for a multi-instance
	// deployment.
	sendLimiter := ratelimit.New()

	sendEmailUsecase := emailusecases.NewSendEmailUsecase(emailusecases.SendEmailDeps{
		ProviderRepo:    providerRepo,
		TemplateRepo:    templateRepo,
		SuppressionRepo: suppressionRepo,
		OutboxRepo:      outboxRepo,
		EventUsecase:    processEventUsecase,
		ProviderFactory: providerFactory,
		Renderer:        templateRenderer,
		BaseURL:         baseURL,
		TrackingSigner:  trackingSigner,
		Limiter:         sendLimiter,
		SendLimits:      emailusecases.NewTenantSendLimits(tenantUsecase),
	})
	emailService := emailservices.NewEmailService(sendEmailUsecase)

	queueWorker := emailusecases.NewQueueWorker(outboxRepo, sendEmailUsecase, manageSuppressionsUsecase, tenantUsecase, 5*time.Second)
	if days := cfg.App.OutboxRetentionDays; days > 0 {
		// Failures are the only outbox rows that accumulate, and each carries
		// the whole serialised request. Without a cutoff this becomes the
		// largest table in the database holding nothing anyone will read.
		queueWorker.SetRetention(time.Duration(days) * 24 * time.Hour)
		slog.Info("outbox retention configured", "days", days)
	}
	sendEmailUsecase.RegisterQueueWorker(queueWorker)
	runWorker(&workers, "outbox-queue", func() { queueWorker.Start(workerCtx) })

	trackingHandler := eventhttp.NewTrackingHandler(processEventUsecase, trackingSigner)

	// One-click unsubscribe (RFC 8058), which Gmail and Yahoo require from bulk
	// senders. It suppresses on POST and only shows a confirmation page on GET —
	// link scanners fetch every URL in a message, so a GET that acted would
	// unsubscribe recipients who never clicked.
	unsubscribeHandler := eventhttp.NewUnsubscribeHandler(
		manageSuppressionsUsecase, processEventUsecase, trackingSigner)

	poller := inboundworker.NewPoller(tenantRepo, providerRepo, inboundUsecase, providerFactory, 30*time.Second)
	runWorker(&workers, "inbound-poller", func() { poller.Start(workerCtx) })

	// IDLE alongside the poll, not instead of it. A hung IDLE is silent — the
	// connection looks open, the server has nothing to say, and inbound stops
	// with nothing in the logs — so the poll stays as the floor under it.
	// Delivering the same message twice is harmless: inbound processing
	// identifies a message by its own Message-ID, so whichever path sees it
	// first wins and the other is a no-op.
	idleSupervisor := inboundworker.NewIdleSupervisor(poller)
	runWorker(&workers, "inbound-idle", func() { idleSupervisor.Start(workerCtx) })

	// What an operator needs to see. Every failure this system has is a quiet
	// one — a stalled outbox still answers 200, a webhook queue that stops
	// draining looks like one with nothing to do, a hung IMAP connection keeps
	// the process healthy — so the depth and, more importantly, the *age* of
	// each queue are published for scraping.
	metrics, err := observability.New()
	if err != nil {
		slog.Error("failed to set up metrics", "error", err)
		os.Exit(1)
	}
	// Installing the global provider is what makes gsmail's own send and
	// receive instrumentation appear; otelgs measures into otel.Meter(...) and
	// was until now recording into a no-op.
	metrics.SetGlobal()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = metrics.Shutdown(shutdownCtx)
	}()

	queueStats := func(read func(context.Context) (int64, time.Time, error)) observability.QueueSource {
		return func(ctx context.Context) (observability.QueueStats, error) {
			pending, oldest, err := read(ctx)
			if err != nil {
				return observability.QueueStats{}, err
			}
			stats := observability.QueueStats{Pending: pending}
			// An empty queue has no oldest item; reporting the age of a
			// sentinel timestamp would look like a very old backlog.
			if pending > 0 && !oldest.IsZero() {
				stats.Oldest = time.Since(oldest)
			}
			return stats, nil
		}
	}

	if err := metrics.ObserveQueue("outbox", "Messages accepted but not yet delivered",
		queueStats(outboxRepo.Stats)); err != nil {
		slog.Error("failed to register outbox metrics", "error", err)
	}
	if err := metrics.ObserveQueue("webhook_deliveries", "Notifications owed to tenant endpoints",
		queueStats(webhookDeliveryRepo.Stats)); err != nil {
		slog.Error("failed to register webhook metrics", "error", err)
	}
	// Zero open sessions while IMAP providers are configured is the signal that
	// inbound has silently stopped.
	if err := metrics.ObserveGauge("imap_idle_sessions", "Open IMAP IDLE connections",
		func() int64 { return int64(idleSupervisor.Sessions()) }); err != nil {
		slog.Error("failed to register idle metrics", "error", err)
	}

	// Its own listener rather than a route on the API. Keeping it off the
	// public surface is the point, and a separate server also means a metrics
	// scrape cannot be starved by the API's own timeouts.
	var metricsServer *http.Server
	if *metricsAddrFlag != "" {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", metrics.Handler())

		// Backup lives here because this is the process that can actually do
		// it: the Pebble stores are held exclusively while the gateway runs,
		// so an external command cannot snapshot them without downtime. The
		// listener is loopback-only, which is the right audience for an
		// operator action and keeps it off the public surface.
		metricsMux.HandleFunc("/admin/backup", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", http.StatusMethodNotAllowed)
				return
			}
			out := r.URL.Query().Get("out")
			if out == "" {
				http.Error(w, "out= is required", http.StatusBadRequest)
				return
			}

			opts := backup.Options{
				OutDir:     out,
				SQLDB:      conn.GetDB(),
				SQLEngine:  cfg.Database.Type,
				SQLitePath: cfg.Database.FilePath,
				Version:    Version,
				Stores:     map[string]backup.Checkpointer{},
			}
			if path, err := config.GetConfigPath(); err == nil {
				opts.ConfigPath = path
			}
			opts.SecretKeyID = keyring.PrimaryKeyID()

			// Every store, or none. A backup missing one of them restores into
			// a gateway that has lost its events or its inbound mail, which is
			// worse than a backup that refused to be taken.
			for name, store := range map[string]any{
				"events.db":  eventRepo,
				"inbound.db": inboundRepo,
				"logs.db":    logStore,
			} {
				cp, ok := store.(backup.Checkpointer)
				if !ok {
					http.Error(w, fmt.Sprintf("%s cannot snapshot itself", name), http.StatusInternalServerError)
					return
				}
				opts.Stores[name] = cp
			}

			manifest, err := backup.Run(r.Context(), opts)
			if err != nil {
				slog.Error("backup failed", "error", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			slog.Info("backup written", "out", out, "contents", manifest.Stores)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(manifest)
		})
		metricsServer = &http.Server{
			Addr:              *metricsAddrFlag,
			Handler:           metricsMux,
			ReadHeaderTimeout: readHeaderTimeout,
		}
		go func() {
			slog.Info("metrics listener started", "addr", *metricsAddrFlag)
			if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				// Not fatal: losing visibility is bad, but refusing to send
				// mail because the metrics port is taken would be worse.
				slog.Error("metrics listener stopped", "error", err)
			}
		}()
	}

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
	interceptors := connect.WithOptions(
		connect.WithInterceptors(rbacInterceptor),

		// connect documents zero — the default, and what this was — as
		// allowing any message size. Every RPC would read whatever it was
		// given into memory before a handler saw it, and SignIn and the setup
		// calls are unauthenticated by design, so that needed no credentials.
		//
		// The compressed case is worse than the raw one: without a limit,
		// decompression is unbounded too, and a few megabytes of gzip expand
		// into as much memory as the attacker cares to name.
		connect.WithReadMaxBytes(maxRPCRequestBytes),
	)

	mux := http.NewServeMux()

	// Every RPC handler is mounted through this one helper, which always
	// applies the interceptor chain. Registering a service without
	// authorization is therefore not something that can be done by
	// forgetting an argument.
	rpc := &rpcRegistrar{mux: mux, interceptors: interceptors}
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return providerconnect.NewHandler(emailProviderService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) { return emailconnect.NewHandler(emailService, o) })
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewAuthServiceHandler(authService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewApiKeyServiceHandler(apiKeyService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewUserServiceHandler(userService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewTenantServiceHandler(tenantService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewSetupServiceHandler(setupService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewTemplateServiceHandler(templateService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewSuppressionServiceHandler(suppressionService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewWebhookServiceHandler(webhookService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewSystemSettingsServiceHandler(settingsService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewEventServiceHandler(eventService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewInboundServiceHandler(inboundService, o)
	})
	rpc.register(func(o connect.Option) (string, http.Handler) {
		return panmailv1connect.NewLogServiceHandler(logService, o)
	})
	mux.Handle(grpchealth.NewHandler(healthChecker))

	// Liveness: is this process still working? The answer being no means
	// restart it, so this deliberately checks nothing external — a database
	// outage must not be answered by restarting every instance at once.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		res, err := healthChecker.Check(r.Context(), &grpchealth.CheckRequest{Service: ""})
		if err != nil || res.Status != grpchealth.StatusServing {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Service Unavailable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Readiness: can this instance serve a request right now? The answer being
	// no means stop sending it traffic. Until this existed, /healthz was the
	// only endpoint and it reported the process, so an instance whose database
	// was unreachable stayed in the load balancer and failed every request it
	// was given.
	readiness := health.New()
	readiness.Register("database", health.SQL(conn.GetDB()))
	mux.HandleFunc("/readyz", readiness.ReadyHandler())
	mux.Handle("/webhooks/", webhookHandler)
	mux.Handle("/inbound/", inboundWebhookHandler)
	mux.HandleFunc("/track/open/", trackingHandler.HandleOpen)
	mux.HandleFunc("/track/click/", trackingHandler.HandleClick)
	mux.Handle("/unsubscribe/", unsubscribeHandler)

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

		// Without these a client can hold a connection open indefinitely by
		// dribbling out a request, and enough of those exhaust the server.
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
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

	// Stop accepting requests first, then let the workers finish what they
	// already claimed, and only then close the stores. Closing Pebble while a
	// worker is still writing to it is a crash, and dropping an in-flight send
	// loses mail the caller was told had been accepted.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if metricsServer != nil {
		_ = metricsServer.Shutdown(shutdownCtx)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown failed", "error", err)
	}

	slog.Info("stopping background workers")
	stopWorkers()

	if waitForWorkers(&workers, workerDrainTimeout) {
		slog.Info("background workers stopped")
	} else {
		slog.Warn("background workers did not stop in time", "timeout", workerDrainTimeout)
	}

	if err := providerFactory.Close(); err != nil {
		slog.Error("failed to close provider connections", "error", err)
	}

	slog.Info("Server stopped gracefully")
}

// rpcRegistrar mounts ConnectRPC handlers with a fixed interceptor chain.
type rpcRegistrar struct {
	mux          *http.ServeMux
	interceptors connect.Option
}

func (r *rpcRegistrar) register(build func(connect.Option) (string, http.Handler)) {
	pattern, handler := build(r.interceptors)
	r.mux.Handle(pattern, handler)
}

// runWorker starts a background worker, tracking it so shutdown can wait for
// it and recovering panics so one worker cannot take the process down.
func runWorker(wg *sync.WaitGroup, name string, fn func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				slog.Error("background worker panicked", "worker", name, "panic", r)
			}
		}()
		fn()
	}()
}

// waitForWorkers reports whether every worker finished within the timeout.
func waitForWorkers(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// newTrackingSigner derives the key that signs open and click links. It is
// bound to the instance's signing key so that links survive restarts.
func newTrackingSigner(cfg *config.Config) (*tracking.Signer, error) {
	if cfg == nil || cfg.Auth.SymmetricKey == "" {
		// Not configured yet: links are only generated once a provider exists,
		// which cannot happen before setup completes.
		return tracking.NewSigner([]byte("panmail-unconfigured-tracking-key")), nil
	}

	key := sha256.Sum256([]byte("panmail-tracking-v1:" + cfg.Auth.SymmetricKey))
	return tracking.NewSigner(key[:]), nil
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
	return migrator.Run(conn.GetDB(), dbType)
}

// handleRotateSecretsCommand rewrites every stored credential under the
// current primary key.
//
// The rotation itself is three steps for an operator, and only the middle one
// is this command:
//
//  1. Generate a key, set it as PANMAIL_SECRET_KEY, and move the old one into
//     PANMAIL_SECRET_KEYS_RETIRED. Restart. New writes use the new key; old
//     values still read.
//  2. Run this. Every value is rewritten under the new key.
//  3. Remove PANMAIL_SECRET_KEYS_RETIRED and restart. The old key is now
//     genuinely unused.
//
// Running it before step 1 is harmless: nothing needs rotating and it reports
// zero. Running it twice is harmless for the same reason.
func handleRotateSecretsCommand() {
	fs := flag.NewFlagSet("rotate-secrets", flag.ExitOnError)
	configFlag := fs.String("config", "", "Path to configuration file")
	_ = fs.Parse(os.Args[2:])

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if *configFlag != "" {
		config.SetConfigPath(*configFlag)
	}

	cfg, err := config.Load()
	if err != nil || cfg == nil {
		fmt.Fprintf(os.Stderr, "cannot rotate secrets: no configuration found (%v)\n", err)
		os.Exit(1)
	}

	// Resolve, never generate. EnsureDataKey invents a key when it finds none
	// and writes it to the config, which is right at first start and exactly
	// wrong here: a rotation is the one operation where an existing key is
	// mandatory, and inventing one turns "I cannot read your data" into "I
	// have silently replaced your key" — leaving every stored credential
	// unreadable with no indication of which key was lost.
	dataKey, err := config.ResolveDataKey(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"cannot rotate secrets: no encryption key is configured. Set %s to the key currently in use.\n",
			secrets.EnvKeyName)
		os.Exit(1)
	}

	retired := secrets.ParseRetiredKeys(os.Getenv(secrets.EnvRetiredKeysName))
	keyring, err := secrets.NewKeyring(dataKey, retired...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot build the keyring: %v\n", err)
		os.Exit(1)
	}

	sqlDB, err := db.Connect(cfg.Database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot connect to the database: %v\n", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	conn := db.NewConnection(sqlDB)

	// Every store that holds an encrypted value, not just the providers.
	//
	// Missing one is the failure that only appears after the old key is
	// dropped: those rows can no longer be decrypted, and whatever depends on
	// them stops working with no obvious cause. Webhook signing secrets were
	// exactly that gap.
	type rotator interface {
		RotateSecrets(ctx context.Context) (int, error)
	}
	stores := []struct {
		name string
		repo any
	}{
		{"provider", postgres.NewStore(conn, keyring)},
		{"webhook", webhookstores.NewStore(conn, keyring)},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	total := 0
	for _, s := range stores {
		r, ok := s.repo.(rotator)
		if !ok {
			fmt.Fprintf(os.Stderr, "this build cannot rotate %s secrets\n", s.name)
			os.Exit(1)
		}
		rotated, err := r.RotateSecrets(ctx)
		total += rotated
		if err != nil {
			// Partial progress is reported rather than hidden: the pass is
			// idempotent, so the operator can fix the cause and run it again.
			fmt.Fprintf(os.Stderr, "rotation stopped in %s secrets after %d row(s): %v\n", s.name, rotated, err)
			os.Exit(1)
		}
		if rotated > 0 {
			fmt.Printf("rotated %d %s secret(s)\n", rotated, s.name)
		}
	}

	fmt.Printf("rotated %d row(s) onto key %s\n", total, keyring.PrimaryKeyID())
	if total == 0 {
		fmt.Println("nothing needed rotating; every stored secret is already on the current key")
	}
	if len(retired) > 0 {
		fmt.Printf("you can now remove %s and restart\n", secrets.EnvRetiredKeysName)
	}
}

// handleBackupCommand writes a consistent snapshot of everything a gateway
// would need to be rebuilt.
//
// Run against a live installation. Both store engines can snapshot themselves
// while being written to, which is the reason this exists rather than a line in
// a runbook telling someone to copy directories: a filesystem copy of an open
// Pebble store or a SQLite file in WAL mode opens cleanly and is quietly
// missing writes.
func handleBackupCommand() {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	configFlag := fs.String("config", "", "Path to configuration file")
	outFlag := fs.String("out", "", "Directory to write the backup into (required)")
	logDirFlag := fs.String("log-dir", "logs.db", "Directory for logs database")
	eventDirFlag := fs.String("event-dir", "events.db", "Directory for events database")
	inboundDirFlag := fs.String("inbound-dir", "inbound.db", "Directory for inbound database")
	_ = fs.Parse(os.Args[2:])

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn})))

	if *outFlag == "" {
		fmt.Fprintln(os.Stderr, "backup: --out is required")
		os.Exit(1)
	}
	if *configFlag != "" {
		config.SetConfigPath(*configFlag)
	}

	cfg, err := config.Load()
	if err != nil || cfg == nil {
		fmt.Fprintf(os.Stderr, "backup: no configuration found (%v)\n", err)
		os.Exit(1)
	}

	sqlDB, err := db.Connect(cfg.Database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: cannot connect to the database: %v\n", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	opts := backup.Options{
		OutDir:     *outFlag,
		SQLDB:      sqlDB,
		SQLEngine:  cfg.Database.Type,
		SQLitePath: cfg.Database.FilePath,
		Version:    Version,
		Stores:     map[string]backup.Checkpointer{},
	}

	if path, err := config.GetConfigPath(); err == nil {
		opts.ConfigPath = path
	}

	// Recorded rather than included: a restore that lands on the wrong key
	// produces unreadable credentials and an error nobody can place, and the
	// fingerprint turns that into a sentence.
	if key, err := config.ResolveDataKey(cfg); err == nil {
		if keyring, err := secrets.NewKeyring(key); err == nil {
			opts.SecretKeyID = keyring.PrimaryKeyID()
		}
	}

	// A running gateway holds these open exclusively, so this only works with
	// it stopped. Silently skipping a store that cannot be opened is how a
	// backup comes to report success while containing a quarter of the data —
	// the failure that this whole command exists to prevent.
	type pebbleStore struct {
		name string
		open func() (interface{ Close() error }, error)
	}
	for _, st := range []pebbleStore{
		{"events.db", func() (interface{ Close() error }, error) { return eventstores.NewStore(*eventDirFlag) }},
		{"inbound.db", func() (interface{ Close() error }, error) { return inboundstores.NewStore(*inboundDirFlag) }},
		{"logs.db", func() (interface{ Close() error }, error) { return logging.NewPebbleStore(*logDirFlag) }},
	} {
		store, err := st.open()
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"backup: cannot open %s: %v\n\n"+
					"These stores are held exclusively by a running gateway. Either stop it and\n"+
					"run this again, or take the backup from the gateway itself:\n"+
					"  curl -X POST 'http://127.0.0.1:9090/admin/backup?out=%s'\n",
				st.name, err, *outFlag)
			os.Exit(1)
		}
		if cp, ok := store.(backup.Checkpointer); ok {
			opts.Stores[st.name] = cp
		} else {
			fmt.Fprintf(os.Stderr, "backup: %s cannot snapshot itself\n", st.name)
			os.Exit(1)
		}
		defer store.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	manifest, err := backup.Run(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("backup written to %s\n", *outFlag)
	fmt.Printf("  contents: %s\n", strings.Join(manifest.Stores, ", "))
	for _, note := range manifest.Notes {
		fmt.Printf("  note: %s\n", note)
	}
}
