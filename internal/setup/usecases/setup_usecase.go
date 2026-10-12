package usecases

import (
	"context"
	crypto_rand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/pkg/auth"
	"github.com/gsoultan/panmail/pkg/db"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// errAlreadySetup is returned by every setup operation once the instance has
// been configured, so the first-run surface closes permanently.
var errAlreadySetup = errors.New("application is already setup")

// supportedEngines are the database engines whose queries actually run.
//
// MySQL and MariaDB are deliberately absent. The DDL layer adapts to them, so
// setup used to succeed and leave an installation where every subsequent query
// failed: the embedded SQL uses PostgreSQL-style `$1` positional parameters,
// which those drivers reject. Refusing here rather than in the UI matters
// because SetupService is a public procedure — the browser is not the only
// thing that can call it.
//
// Adding an engine to this list requires a placeholder-rebinding layer and
// per-vendor coverage, not just an entry.
var supportedEngines = map[string]bool{
	"postgres": true,
	"sqlite":   true,
}

func validateEngine(engine string) error {
	if supportedEngines[engine] {
		return nil
	}
	if engine == "mysql" || engine == "mariadb" {
		return fmt.Errorf("%s is not supported: Panmail's queries use PostgreSQL-style positional parameters, "+
			"which that driver rejects. Use postgres or sqlite", engine)
	}
	return fmt.Errorf("unsupported database type: %q. Use postgres or sqlite", engine)
}

type SetupUsecase interface {
	IsSetup(ctx context.Context) (bool, error)
	Setup(ctx context.Context, dbCfg *panmailv1.DatabaseConfig, adminEmail, adminPassword, adminName, baseURL string) error
	TestDatabaseConnection(ctx context.Context, dbCfg *panmailv1.DatabaseConfig) error
}

type setupUsecase struct {
	authUsecase    usecases.AuthUsecase
	conn           db.Connection
	tokenMaker     *auth.SwappableTokenMaker
	trackingSigner *tracking.Signer
	// dataKey is the key the running process already encrypts credentials
	// with, chosen at boot, and written to the config so a restart keeps
	// reading what was stored before it. Empty when the key comes from the
	// environment, which stays out of the file.
	dataKey   string
	migrateFn func(db.Connection, string) error

	// saveConfig writes the configuration. A field so that a test can make
	// the last step of setup fail.
	saveConfig func(*config.Config) error
}

func NewSetupUsecase(
	authUsecase usecases.AuthUsecase,
	conn db.Connection,
	tokenMaker *auth.SwappableTokenMaker,
	trackingSigner *tracking.Signer,
	dataKey string,
	migrateFn func(db.Connection, string) error,
) SetupUsecase {
	return &setupUsecase{
		authUsecase:    authUsecase,
		conn:           conn,
		tokenMaker:     tokenMaker,
		trackingSigner: trackingSigner,
		dataKey:        dataKey,
		migrateFn:      migrateFn,
		saveConfig:     config.Save,
	}
}

// IsSetup reports whether the instance has been configured.
//
// The configuration file is the marker, and Setup writes it last, so its
// existence means every earlier step — the database, the migrations and the
// first administrator — succeeded. Only a file that is definitely absent means
// "not set up": any other failure to look is returned, because answering "not
// set up" on, say, a permission error would reopen the unauthenticated
// first-run surface on a configured instance.
func (u *setupUsecase) IsSetup(ctx context.Context) (bool, error) {
	path, err := config.GetConfigPath()
	if err != nil {
		return false, err
	}

	_, err = os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("could not tell whether the instance is set up: %w", err)
	}
}

// setupMu serialises Setup across the process. Setup is public and its "not
// yet configured" check is only meaningful if nobody else is between that
// check and the write that ends it; two wizards submitted at once would
// otherwise both create an administrator and race to write the configuration.
// Package level rather than per usecase because the file it guards is
// process wide too.
var setupMu sync.Mutex

func (u *setupUsecase) Setup(ctx context.Context, dbCfg *panmailv1.DatabaseConfig, adminEmail, adminPassword, adminName, baseURL string) error {
	setupMu.Lock()
	defer setupMu.Unlock()

	isSetup, err := u.IsSetup(ctx)
	if err != nil {
		return err
	}
	if isSetup {
		return errAlreadySetup
	}

	// Everything that can be refused without side effects is refused first.
	// A password the administrator rule would reject used to be discovered
	// only after the configuration was written, which closed setup for good
	// on an instance with no user able to sign in.
	if dbCfg == nil {
		return errors.New("database configuration is required")
	}
	if err := validateEngine(dbCfg.Type); err != nil {
		return err
	}
	admin := usecases.NewAdmin{
		Email:    adminEmail,
		Password: adminPassword,
		Name:     adminName,
	}
	if err := usecases.ValidateNewAdmin(admin); err != nil {
		return err
	}

	cfg := db.Config{
		Type:     dbCfg.Type,
		Host:     dbCfg.Host,
		Port:     int(dbCfg.Port),
		User:     dbCfg.User,
		Password: dbCfg.Password,
		DBName:   dbCfg.Dbname,
		FilePath: dbCfg.FilePath,
	}

	newDB, err := db.Connect(cfg)
	if err != nil {
		return fmt.Errorf("failed to connect to new database: %w", err)
	}

	// The administrator is written through the shared connection, so it has
	// to point at the new database before anything else can happen. Until the
	// configuration is saved this is not a commitment: a failed attempt
	// leaves setup open, and the next one replaces the connection again.
	u.conn.SetDB(newDB)

	if err := u.migrateFn(u.conn, cfg.Type); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	symmetricKey := make([]byte, 32)
	if _, err := crypto_rand.Read(symmetricKey); err != nil {
		return fmt.Errorf("failed to generate symmetric key: %w", err)
	}

	appCfg := &config.Config{
		Database: cfg,
		Auth: config.AuthConfig{
			SymmetricKey: hex.EncodeToString(symmetricKey),
		},
		App: config.AppConfig{
			BaseURL: baseURL,
		},
		Secrets: config.SecretsConfig{
			DataKey: u.dataKey,
		},
	}

	created, err := u.authUsecase.CreateAdmin(ctx, admin)
	if err != nil {
		return fmt.Errorf("failed to create the administrator: %w", err)
	}

	// Last, because the file's existence is what closes setup. Written before
	// the administrator — the old order — any failure above left an instance
	// that refused setup and had nobody who could sign in.
	if err := u.saveConfig(appCfg); err != nil {
		// Without the file setup is still open, so take the administrator
		// back out: otherwise the retry fails on the address it already used.
		if rmErr := u.authUsecase.RemoveAdmin(ctx, created); rmErr != nil {
			return errors.Join(fmt.Errorf("failed to save configuration: %w", err),
				fmt.Errorf("could not remove the administrator it had created: %w", rmErr))
		}
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	if u.tokenMaker != nil {
		if maker, err := auth.NewPasetoMaker(appCfg.Auth.SymmetricKey); err == nil {
			u.tokenMaker.SetMaker(maker)
		}
	}

	// The tracking signer has to move to the new key for the same reason the
	// token maker does, but the consequence of forgetting differs. A stale token
	// maker logs somebody out and they sign in again; a stale tracking signer
	// keeps minting links that verify until the next restart and never again,
	// and those links are already in inboxes by then. Every click answers 403
	// and every one-click unsubscribe is refused while panmail keeps sending.
	if u.trackingSigner != nil {
		u.trackingSigner.SetKey(tracking.DeriveKey(appCfg.Auth.SymmetricKey))
	}

	return nil
}

// TestDatabaseConnection dials the supplied database so the setup wizard can
// report whether the details work.
//
// It is only available before the instance is configured. Left open it would
// be a standing, unauthenticated way to make this server connect to any host
// and port the caller names and report what happened.
func (u *setupUsecase) TestDatabaseConnection(ctx context.Context, dbCfg *panmailv1.DatabaseConfig) error {
	isSetup, err := u.IsSetup(ctx)
	if err != nil {
		return err
	}
	if isSetup {
		return errAlreadySetup
	}

	// Checked before dialling, so the wizard's "test connection" reports the
	// engine as unsupported instead of reporting success on a database that
	// would then fail every query.
	if err := validateEngine(dbCfg.Type); err != nil {
		return err
	}

	cfg := db.Config{
		Type:     dbCfg.Type,
		Host:     dbCfg.Host,
		Port:     int(dbCfg.Port),
		User:     dbCfg.User,
		Password: dbCfg.Password,
		DBName:   dbCfg.Dbname,
		FilePath: dbCfg.FilePath,
	}

	testDB, err := db.Connect(cfg)
	if err != nil {
		// The driver's error can echo the DSN back, credentials included.
		return fmt.Errorf("could not connect to the %s database with these details", cfg.Type)
	}
	defer testDB.Close()

	return nil
}
