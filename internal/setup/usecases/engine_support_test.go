package usecases

import (
	"context"
	"os"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// newUnconfiguredUsecase returns a setup usecase whose config file does not
// exist, so the first-run surface is still open and the engine check is the
// thing under test rather than errAlreadySetup.
func newUnconfiguredUsecase(t *testing.T) SetupUsecase {
	t.Helper()
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	if _, err := os.Stat(tempHome + "/.panmail/db_config.yaml"); err == nil {
		t.Fatal("expected no config file in the temporary home")
	}
	return NewSetupUsecase(&mockAuthUsecase{isFirstRun: true}, &mockConnection{}, nil, nil)
}

// Choosing MySQL or MariaDB used to succeed: the DDL layer substitutes types
// per engine, so migrations ran, and only afterwards did every query fail
// because the embedded SQL uses `$1` placeholders those drivers reject. The
// result was an installation that looked configured and did nothing.
//
// Rejection has to happen in the usecase, not just the wizard, because
// SetupService.Setup is a public procedure.
func TestSetupRejectsEnginesWhoseQueriesCannotRun(t *testing.T) {
	for _, engine := range []string{"mysql", "mariadb"} {
		t.Run(engine, func(t *testing.T) {
			u := newUnconfiguredUsecase(t)

			err := u.Setup(context.Background(),
				&panmailv1.DatabaseConfig{
					Type: engine, Host: "127.0.0.1", Port: 3306,
					User: "root", Password: "root", Dbname: "panmail",
				},
				"admin@example.com", "a-long-enough-password", "Admin", "http://localhost")

			if err == nil {
				t.Fatalf("Setup with %s must be rejected", engine)
			}
			if !strings.Contains(err.Error(), "not supported") {
				t.Errorf("expected an unsupported-engine error, got: %v", err)
			}
		})
	}
}

// The wizard's "test connection" button must not report success for an engine
// the instance cannot then use, so the same guard runs before dialling.
func TestTestDatabaseConnectionRejectsUnsupportedEngines(t *testing.T) {
	for _, engine := range []string{"mysql", "mariadb", "oracle"} {
		t.Run(engine, func(t *testing.T) {
			u := newUnconfiguredUsecase(t)

			err := u.TestDatabaseConnection(context.Background(),
				&panmailv1.DatabaseConfig{
					Type: engine, Host: "127.0.0.1", Port: 3306,
					User: "root", Password: "root", Dbname: "panmail",
				})

			if err == nil {
				t.Fatalf("TestDatabaseConnection with %s must be rejected", engine)
			}
			if !strings.Contains(err.Error(), "supported") {
				t.Errorf("expected an unsupported-engine error, got: %v", err)
			}
		})
	}
}

// The guard must not narrow what actually works.
func TestSupportedEnginesAreAccepted(t *testing.T) {
	for _, engine := range []string{"postgres", "sqlite"} {
		if err := validateEngine(engine); err != nil {
			t.Errorf("%s must be accepted, got: %v", engine, err)
		}
	}
}
