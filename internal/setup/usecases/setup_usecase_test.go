package usecases

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/entities"
)

type mockAuthUsecase struct {
	isFirstRun    bool
	isFirstRunErr error
}

func (m *mockAuthUsecase) SignIn(ctx context.Context, email, password string) (*entities.User, string, bool, bool, string, string, error) {
	return nil, "", false, false, "", "", nil
}
func (m *mockAuthUsecase) GetCurrentUser(ctx context.Context, userID string) (*entities.User, error) {
	return nil, nil
}
func (m *mockAuthUsecase) CreateAdmin(ctx context.Context, email, password, name string) error {
	return nil
}
func (m *mockAuthUsecase) IsFirstRun(ctx context.Context) (bool, error) {
	return m.isFirstRun, m.isFirstRunErr
}
func (m *mockAuthUsecase) SetupTwoFactor(ctx context.Context, userID string) (string, string, error) {
	return "", "", nil
}
func (m *mockAuthUsecase) VerifyTwoFactor(ctx context.Context, userID, email, code, secret string) (string, *entities.User, bool, error) {
	return "", nil, false, nil
}
func (m *mockAuthUsecase) EnableTwoFactor(ctx context.Context, userID, code, secret string) error {
	return nil
}
func (m *mockAuthUsecase) DisableTwoFactor(ctx context.Context, userID string) error {
	return nil
}

type mockConnection struct {
	isConnected bool
}

func (m *mockConnection) GetDB() *sql.DB   { return nil }
func (m *mockConnection) SetDB(db *sql.DB) {}
func (m *mockConnection) IsConnected() bool {
	return m.isConnected
}

func TestIsSetup(t *testing.T) {
	// Setup temporary home directory
	tempHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", oldHome)

	configDir := filepath.Join(tempHome, ".panmail")
	configPath := filepath.Join(configDir, "db_config.yaml")

	tests := []struct {
		name          string
		hasConfigFile bool
		expectedSetup bool
	}{
		{
			name:          "Config file exists",
			hasConfigFile: true,
			expectedSetup: true,
		},
		{
			name:          "Config file does not exist",
			hasConfigFile: false,
			expectedSetup: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Prepare environment
			os.RemoveAll(configDir)
			if tc.hasConfigFile {
				err := os.MkdirAll(configDir, 0755)
				if err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(configPath, []byte("database: {type: sqlite}"), 0600)
				if err != nil {
					t.Fatal(err)
				}
			}

			authUsecase := &mockAuthUsecase{}
			conn := &mockConnection{}
			u := NewSetupUsecase(authUsecase, conn, nil, nil)

			got, err := u.IsSetup(context.Background())
			if err != nil {
				t.Fatalf("IsSetup() error = %v", err)
			}
			if got != tc.expectedSetup {
				t.Errorf("IsSetup() = %v, want %v", got, tc.expectedSetup)
			}
		})
	}
}

func TestSetupPreventedIfAlreadySetup(t *testing.T) {
	// Setup temporary home directory
	tempHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", oldHome)

	configDir := filepath.Join(tempHome, ".panmail")
	configPath := filepath.Join(configDir, "db_config.yaml")

	// Create config file to simulate "already setup"
	err := os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(configPath, []byte("database: {type: sqlite}"), 0600)
	if err != nil {
		t.Fatal(err)
	}

	authUsecase := &mockAuthUsecase{isFirstRun: false}
	conn := &mockConnection{isConnected: true}
	u := NewSetupUsecase(authUsecase, conn, nil, nil)

	err = u.Setup(context.Background(), &panmailv1.DatabaseConfig{}, "admin@example.com", "password", "Admin", "http://localhost")
	if err == nil {
		t.Fatal("expected error when calling Setup on already setup application, got nil")
	}

	expectedErr := "application is already setup"
	if err.Error() != expectedErr {
		t.Errorf("expected error %q, got %q", expectedErr, err.Error())
	}
}
