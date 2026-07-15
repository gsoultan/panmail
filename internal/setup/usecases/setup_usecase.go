package usecases

import (
	"context"
	crypto_rand "crypto/rand"
	"encoding/hex"
	"fmt"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/pkg/auth"
	"github.com/gsoultan/panmail/pkg/db"
	"os"
)

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
		return fmt.Errorf("application is already setup")
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
	return u.authUsecase.CreateAdmin(ctx, adminEmail, adminPassword, adminName)
}

func (u *setupUsecase) TestDatabaseConnection(ctx context.Context, dbCfg *panmailv1.DatabaseConfig) error {
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
		return err
	}
	defer testDB.Close()

	return nil
}
