package usecases

import (
	"context"
	crypto_rand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/pkg/auth"
	"github.com/gsoultan/panmail/pkg/db"
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
	authUsecase usecases.AuthUsecase
	conn        db.Connection
	tokenMaker  *auth.SwappableTokenMaker
	migrateFn   func(db.Connection, string) error
}

func NewSetupUsecase(
	authUsecase usecases.AuthUsecase,
	conn db.Connection,
	tokenMaker *auth.SwappableTokenMaker,
	migrateFn func(db.Connection, string) error,
) SetupUsecase {
	return &setupUsecase{
		authUsecase: authUsecase,
		conn:        conn,
		tokenMaker:  tokenMaker,
		migrateFn:   migrateFn,
	}
}

func (u *setupUsecase) IsSetup(ctx context.Context) (bool, error) {
	// Check if config file exists. If it exists, we consider it setup.
	path, err := config.GetConfigPath()
	if err != nil {
		return false, err
	}

	if _, err := os.Stat(path); err == nil {
		return true, nil
	}

	return false, nil
}

func (u *setupUsecase) Setup(ctx context.Context, dbCfg *panmailv1.DatabaseConfig, adminEmail, adminPassword, adminName, baseURL string) error {
	// Check if already setup
	isSetup, err := u.IsSetup(ctx)
	if err == nil && isSetup {
		return errAlreadySetup
	}

	if err := validateEngine(dbCfg.Type); err != nil {
		return err
	}

	// 1. Connect to new DB
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

	// 2. Set the connection
	u.conn.SetDB(newDB)

	// 3. Run migrations
	if err := u.migrateFn(u.conn, cfg.Type); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// 4. Save config
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
	}
	if err := config.Save(appCfg); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	// 5. Update token maker
	maker, err := auth.NewPasetoMaker(appCfg.Auth.SymmetricKey)
	if err == nil {
		u.tokenMaker.SetMaker(maker)
	}

	// 6. Create admin user
	return u.authUsecase.CreateAdmin(ctx, usecases.NewAdmin{
		Email:    adminEmail,
		Password: adminPassword,
		Name:     adminName,
	})
}

// TestDatabaseConnection dials the supplied database so the setup wizard can
// report whether the details work.
//
// It is only available before the instance is configured. Left open it would
// be a standing, unauthenticated way to make this server connect to any host
// and port the caller names and report what happened.
func (u *setupUsecase) TestDatabaseConnection(ctx context.Context, dbCfg *panmailv1.DatabaseConfig) error {
	if isSetup, err := u.IsSetup(ctx); err == nil && isSetup {
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
