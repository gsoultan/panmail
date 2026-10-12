package usecases

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/pkg/auth"
	"github.com/gsoultan/panmail/pkg/db"
)

// The configuration file is what closes setup, so it may only appear once
// everything before it has worked. It used to be written before the first
// administrator, and any refusal from there on — a password the admin rule
// rejected, a duplicate address, a database error — left an instance that
// answered "application is already setup" and had nobody who could sign in.

// configuredAt points the config path into a fresh directory and returns it.
func configuredAt(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config.SetConfigPath(filepath.Join(dir, "db_config.yaml"))
	t.Cleanup(func() { config.SetConfigPath("") })
	return dir
}

func sqliteConfig(dir string) *panmailv1.DatabaseConfig {
	return &panmailv1.DatabaseConfig{Type: "sqlite", FilePath: filepath.Join(dir, "panmail.db")}
}

func configExists(t *testing.T) bool {
	t.Helper()
	path, err := config.GetConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(path)
	return err == nil
}

func newOrderedUsecase(authUsecase *mockAuthUsecase, conn *mockConnection) SetupUsecase {
	return NewSetupUsecase(authUsecase, conn, auth.NewSwappableTokenMaker(nil), nil, "",
		func(db.Connection, string) error { return nil })
}

func TestSetupRefusesBadAdministratorDetailsBeforeAnySideEffect(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		password string
	}{
		// Six characters is what the wizard used to accept; the administrator
		// rule wants twelve.
		{"password the wizard used to allow", "admin@example.com", "secret"},
		{"empty password", "admin@example.com", ""},
		{"no email address", "", "a-long-enough-password"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := configuredAt(t)
			authUsecase := &mockAuthUsecase{isFirstRun: true}
			conn := &mockConnection{}
			u := newOrderedUsecase(authUsecase, conn)

			if err := u.Setup(context.Background(), sqliteConfig(dir), tc.email, tc.password, "Admin", "http://localhost"); err == nil {
				t.Fatal("expected setup to refuse the administrator's details")
			}
			if configExists(t) {
				t.Fatal("setup wrote its configuration for a request it refused: the instance is now locked with no administrator")
			}
			if conn.setDBCalls.Load() != 0 {
				t.Error("setup switched the database connection before validating its input")
			}
			if _, err := os.Stat(filepath.Join(dir, "panmail.db")); err == nil {
				t.Error("setup created the database file before validating its input")
			}
			if open, err := u.IsSetup(context.Background()); err != nil || open {
				t.Errorf("IsSetup() = %v, %v; want false — setup must stay open after a refusal", open, err)
			}
		})
	}
}

func TestAFailedAdministratorLeavesSetupOpen(t *testing.T) {
	dir := configuredAt(t)
	authUsecase := &mockAuthUsecase{isFirstRun: true, createErr: errors.New("duplicate email")}
	u := newOrderedUsecase(authUsecase, &mockConnection{})

	if err := u.Setup(context.Background(), sqliteConfig(dir), "admin@example.com", "a-long-enough-password", "Admin", "http://localhost"); err == nil {
		t.Fatal("expected the administrator failure to fail setup")
	}
	if configExists(t) {
		t.Fatal("configuration written although no administrator exists")
	}

	// And the retry, once the cause is gone, is allowed to finish.
	authUsecase.createErr = nil
	if err := u.Setup(context.Background(), sqliteConfig(dir), "admin@example.com", "a-long-enough-password", "Admin", "http://localhost"); err != nil {
		t.Fatalf("retry after a failed attempt: %v", err)
	}
	if !configExists(t) {
		t.Fatal("a successful setup wrote no configuration")
	}
}

func TestTheConfigurationIsWrittenAfterTheAdministrator(t *testing.T) {
	dir := configuredAt(t)
	authUsecase := &mockAuthUsecase{isFirstRun: true}
	authUsecase.onCreate = func() {
		if configExists(t) {
			t.Error("the configuration already existed when the administrator was created")
		}
	}
	u := newOrderedUsecase(authUsecase, &mockConnection{})

	if err := u.Setup(context.Background(), sqliteConfig(dir), "admin@example.com", "a-long-enough-password", "Admin", "http://localhost"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if authUsecase.created.Load() != 1 {
		t.Errorf("CreateAdmin succeeded %d times, want 1", authUsecase.created.Load())
	}
}

// If the configuration cannot be saved, setup is still open, so the
// administrator it created is taken back out; otherwise the retry would fail
// on the address the failed attempt already used.
func TestASaveFailureRemovesTheAdministrator(t *testing.T) {
	dir := configuredAt(t)
	authUsecase := &mockAuthUsecase{isFirstRun: true}
	u := newOrderedUsecase(authUsecase, &mockConnection{})
	u.(*setupUsecase).saveConfig = func(*config.Config) error { return errors.New("disk full") }

	if err := u.Setup(context.Background(), sqliteConfig(dir), "admin@example.com", "a-long-enough-password", "Admin", "http://localhost"); err == nil {
		t.Fatal("expected setup to fail when the configuration cannot be saved")
	}
	if authUsecase.removed.Load() != 1 {
		t.Errorf("RemoveAdmin called %d times after a failed save, want 1", authUsecase.removed.Load())
	}
}

// "Could not look" is not "not set up". Answering false on an unreadable path
// would reopen the unauthenticated setup surface on a configured instance.
func TestIsSetupReportsAPathItCannotCheck(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Stat through a regular file fails with ENOTDIR, which is neither "exists"
	// nor "does not exist".
	config.SetConfigPath(filepath.Join(file, "db_config.yaml"))
	t.Cleanup(func() { config.SetConfigPath("") })

	authUsecase := &mockAuthUsecase{isFirstRun: true}
	u := newOrderedUsecase(authUsecase, &mockConnection{})

	if _, err := u.IsSetup(context.Background()); err == nil {
		t.Fatal("IsSetup() returned no error for a path it could not check")
	}
	if err := u.Setup(context.Background(), sqliteConfig(dir), "admin@example.com", "a-long-enough-password", "Admin", "http://localhost"); err == nil {
		t.Fatal("Setup ran although it could not tell whether the instance was already set up")
	}
	if authUsecase.created.Load() != 0 {
		t.Error("an administrator was created on an instance whose setup state is unknown")
	}
}

// Two wizards submitted at once must not both get through the "not yet set up"
// check: that is two administrators and two configurations racing to the disk.
func TestConcurrentSetupsRunOneAtATime(t *testing.T) {
	dir := configuredAt(t)
	authUsecase := &mockAuthUsecase{isFirstRun: true}
	// Widen the window between the check and the write, which is where two
	// unserialised calls would both pass.
	authUsecase.onCreate = func() { time.Sleep(20 * time.Millisecond) }
	u := newOrderedUsecase(authUsecase, &mockConnection{})

	const callers = 4
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			errs[i] = u.Setup(context.Background(), sqliteConfig(dir), "admin@example.com", "a-long-enough-password", "Admin", "http://localhost")
		})
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case !errors.Is(err, errAlreadySetup):
			t.Errorf("unexpected error from a concurrent setup: %v", err)
		}
	}
	if succeeded != 1 {
		t.Errorf("%d concurrent setups succeeded, want exactly 1", succeeded)
	}
	if got := authUsecase.created.Load(); got != 1 {
		t.Errorf("%d administrators created, want 1", got)
	}
}
